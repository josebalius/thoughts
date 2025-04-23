package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

const wrapper = `
<!DOCTYPE html>
<html>
	<head>
		<title>{{.Title}}</title>
		<style type="text/css">
			body {
				font-family: monospace;
			}
			.content {
				margin: 0 auto;
				width: 800px;
				border: 1px solid #888;
				padding: 20px;
				box-shadow: 2px 2px #ccc;
			}
		</style>
	</head>
	<body>
		<div class="content">
			{{.Body}}
		</div>
	</body>
</html>
`

type site struct {
	title              string
	logger             *log.Logger
	activeRepo         *repo
	versionA, versionB *repo
	tpl                *template.Template
}

func newSite(logger *log.Logger, repoURL, siteTitle string, useCache bool) (*site, error) {
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

	t, err := template.New("wrapper").Parse(wrapper)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	repoA := newRepo(fp)

	return &site{
		title:      siteTitle,
		logger:     logger,
		activeRepo: repoA,
		versionA:   repoA,
		versionB:   newRepo(fp),
		tpl:        t,
	}, nil
}

func (s *site) Serve(ctx context.Context) error {
	s.logger.Println("syncing active repo")
	if err := s.activeRepo.Sync(ctx); err != nil {
		return fmt.Errorf("failed to sync repo: %w", err)
	}

	g, ctx := errgroup.WithContext(ctx)

	// Run syncRepos in a goroutine, but do not let its error stop Serve
	g.Go(func() error {
		err := s.syncRepos(ctx)
		if err != nil {
			s.logger.Printf("failed to sync repos: %v", err)
		}
		return nil // always return nil so Serve doesn't stop
	})

	g.Go(func() error {
		s.logger.Println("starting server on :8080")
		server := &http.Server{
			Addr:    ":8080",
			Handler: s,
		}

		shutdown := func() {
			<-ctx.Done()
			shutdownctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownctx); err != nil {
				s.logger.Printf("failed to shutdown server: %v", err)
			}
		}
		go shutdown()

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	})

	return g.Wait()
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if err := recover(); err != nil {
			fmt.Println("recovered from panic:", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}()

	if r.URL.Path == "/" {
		s.serveIndex(w, r)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	doc, ok := s.activeRepo.Document(path)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	s.serve(w, r, doc)
}

func (s *site) serve(w http.ResponseWriter, r *http.Request, doc *document) {
	b, err := s.renderDocument(doc)
	if err != nil {
		fmt.Println("failed to render document:", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func (s *site) serveIndex(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, s.activeRepo.Index())
}

func (s *site) renderDocument(doc *document) ([]byte, error) {
	contents, err := doc.Render()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	if err := s.tpl.Execute(&buf, struct {
		Title string
		Body  template.HTML
	}{
		Title: s.title,
		Body:  template.HTML(contents),
	}); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
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
}
