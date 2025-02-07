package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type site struct {
	logger             *log.Logger
	activeRepo         *repo
	versionA, versionB *repo
}

func newSite(logger *log.Logger, repoURL string, useCache bool) (*site, error) {
	logger.Printf("creating site for %s\n", repoURL)

	var fp fileProvider

	ghclient, err := newGitHubClient(logger, githubAPI, repoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create github client: %w", err)
	}
	fp = ghclient

	if useCache {
		logger.Println("using cached github client")
		cachedClient, err := newCachedGitHubClient(logger, ghclient)
		if err != nil {
			return nil, fmt.Errorf("failed to create cached github client: %w", err)
		}
		fp = cachedClient
	}

	repoA := newRepo(fp)

	return &site{
		logger:     logger,
		activeRepo: repoA,
		versionA:   repoA,
		versionB:   newRepo(fp),
	}, nil
}

func (s *site) Serve(ctx context.Context) error {
	server := &http.Server{
		Addr:    ":8080",
		Handler: s,
	}

	s.logger.Println("syncing active repo")
	if err := s.activeRepo.Sync(ctx); err != nil {
		return fmt.Errorf("failed to sync repo: %w", err)
	}

	syncerr := make(chan error, 1)
	go func() {
		if err := s.syncRepos(ctx); err != nil {
			syncerr <- err
		}
	}()

	s.logger.Println("starting server on :8080")
	servererr := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil {
			servererr <- err
		}
	}()

	for {
		select {
		case <-ctx.Done():
			shutdownctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownctx); err != nil {
				return fmt.Errorf("failed to shutdown server: %w", err)
			}

			return nil
		case err := <-syncerr:
			// sync errors should be logged, but not fatal
			fmt.Printf("failed to sync repos: %v\n", err)
		case err := <-servererr:
			return fmt.Errorf("failed to serve: %w", err)
		}
	}
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		s.serveIndex(w, r)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	doc := s.activeRepo.Document(path)
	if doc == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	doc.Serve(w, r)
}

func (s *site) serveIndex(w http.ResponseWriter, r *http.Request) {
	s.activeRepo.Index().Serve(w, r)
}

func (s *site) syncRepos(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return nil

		case <-ticker.C:
			switch s.activeRepo {
			case s.versionA:
				if err := s.versionB.Sync(ctx); err != nil {
					return fmt.Errorf("failed to sync repo B: %w", err)
				}
				s.activeRepo = s.versionB
			case s.versionB:
				if err := s.versionA.Sync(ctx); err != nil {
					return fmt.Errorf("failed to sync repo A: %w", err)
				}
				s.activeRepo = s.versionA
			}
		}
	}

	return nil
}
