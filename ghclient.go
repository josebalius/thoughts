package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const githubAPI = "https://api.github.com"

type githubClient struct {
	logger *log.Logger
	apiURL string
	client *http.Client
	owner  string
	name   string
}

func newGitHubClient(logger *log.Logger, apiURL, repoURL string) (*githubClient, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse url: %w", err)
	}

	p := strings.Split(u.Path, "/")
	if len(p) != 3 {
		return nil, errors.New("invalid repo url, should be just github.com/{owner}/{name}")
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	logger.Printf("nwo: %s/%s\n", p[1], p[2])
	return &githubClient{
		logger: logger,
		apiURL: apiURL,
		client: client,
		owner:  p[1],
		name:   p[2],
	}, nil
}

func (g *githubClient) LastHash(ctx context.Context) (string, error) {
	activityURL := fmt.Sprintf("%s/repos/%s/%s/activity", g.apiURL, g.owner, g.name)
	req, err := http.NewRequestWithContext(ctx, "GET", activityURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "thoughts-agent")

	g.logger.Printf("getting last hash %s\n", activityURL)
	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var response []struct {
		After string `json:"after"`
	}
	if err := json.Unmarshal(b, &response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(response) == 0 {
		return "", errors.New("no activity found, must commit to the repo before using the agent")
	}

	g.logger.Printf("last hash is %s\n", response[0].After)
	return response[0].After, nil
}

func (g *githubClient) Contents(ctx context.Context) (fs.FS, func(), error) {
	zipURL := fmt.Sprintf("%s/repos/%s/%s/zipball/main", g.apiURL, g.owner, g.name)
	req, err := http.NewRequestWithContext(ctx, "GET", zipURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "thoughts-agent")

	g.logger.Printf("getting zipball %s\n", zipURL)
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to do request: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		return nil, nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response: %w", err)
	}

	g.logger.Printf("zipball is %d bytes\n", len(b))
	r, err := zip.NewReader(bytes.NewReader(b), resp.ContentLength)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create zip reader: %w", err)
	}

	return r, func() {
		resp.Body.Close()
	}, nil
}

const cacheDir = "cache"

type cachedGitHubClient struct {
	logger   *log.Logger
	client   *githubClient
	destRoot string
}

func newCachedGitHubClient(logger *log.Logger, c *githubClient) (*cachedGitHubClient, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	return &cachedGitHubClient{
		logger: logger, client: c, destRoot: filepath.Join(wd, cacheDir),
	}, nil
}

func (c *cachedGitHubClient) LastHash(ctx context.Context) (string, error) {
	if _, ok := c.cacheExists(); ok {
		c.logger.Println("cache exists")
		return "cached-hash", nil // all we need is a stable hash
	}

	hash, err := c.client.LastHash(ctx)
	if err != nil {
		return "", err
	}

	return hash, nil
}

func (c *cachedGitHubClient) cacheExists() (fs.FS, bool) {
	if _, err := os.Stat(c.destRoot); err != nil {
		return nil, false
	}

	root := filepath.Join(c.destRoot, "..")
	ghFS, err := fs.Sub(os.DirFS(root), cacheDir)
	if err != nil {
		return nil, false
	}

	return ghFS, true
}

func (c *cachedGitHubClient) Contents(ctx context.Context) (fs.FS, func(), error) {
	if ghFS, ok := c.cacheExists(); ok {
		c.logger.Println("using cache for contents")
		return ghFS, func() {}, nil
	}

	ghFS, cleanup, err := c.client.Contents(ctx)
	if err != nil {
		return nil, nil, err
	}

	c.logger.Println("caching contents")
	err = fs.WalkDir(ghFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Construct the destination path.
		destPath := filepath.Join(c.destRoot, path)

		if d.IsDir() {
			// Create the directory (if it doesn't exist).
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory %q: %w", destPath, err)
			}
			return nil
		}

		// Ensure the directory for the file exists.
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create parent directory for %q: %w", destPath, err)
		}

		// Open the source file.
		srcFile, err := ghFS.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open source file %q: %w", path, err)
		}
		defer srcFile.Close()

		// Create the destination file.
		dstFile, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("failed to create destination file %q: %w", destPath, err)
		}
		defer dstFile.Close()

		// Copy the content from the source file to the destination file.
		if _, err := io.Copy(dstFile, srcFile); err != nil {
			return fmt.Errorf("failed to copy %q to %q: %w", path, destPath, err)
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return ghFS, cleanup, nil
}
