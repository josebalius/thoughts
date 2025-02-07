package main

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

type fileProvider interface {
	LastHash(ctx context.Context) (string, error)
	Contents(ctx context.Context) (fs.FS, func(), error)
}

type repo struct {
	fp        fileProvider
	hash      string
	index     *document
	documents map[string]*document
}

func newRepo(fp fileProvider) *repo {
	return &repo{fp: fp, documents: make(map[string]*document)}
}

func (r *repo) Sync(ctx context.Context) error {
	hash, err := r.fp.LastHash(ctx)
	if err != nil {
		return fmt.Errorf("failed to get last hash: %w", err)
	}

	if hash == r.hash {
		return nil
	}

	repoFS, cleanup, err := r.fp.Contents(ctx)
	if err != nil {
		return fmt.Errorf("failed to get contents: %w", err)
	}
	defer cleanup()

	docs, err := r.extractDocuments(repoFS)
	if err != nil {
		return fmt.Errorf("failed to extract documents: %w", err)
	}

	return r.indexDocuments(docs)
}

func (r *repo) Index() *document {
	return r.index
}

func (r *repo) Document(path string) *document {
	return r.documents[path]
}

func (r *repo) indexDocuments(docs []*document) error {
	for _, d := range docs {
		if d.path == "README.md" {
			r.index = d
			continue
		}

		p := strings.TrimSuffix(d.path, ".md")
		r.documents[p] = d
	}

	if r.index == nil {
		return fmt.Errorf("no index document found")
	}

	return nil
}

func (r *repo) extractDocuments(repo fs.FS) ([]*document, error) {
	var documents []*document
	err := fs.WalkDir(repo, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to walk dir: %w", err)
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() != "README.md" && !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		contents, err := fs.ReadFile(repo, path)
		if err != nil {
			return fmt.Errorf("failed to read file: %w", err)
		}

		p := strings.Split(path, string(filepath.Separator))
		p = p[1:]
		path = strings.Join(p, string(filepath.Separator))

		document, err := newDocument(path, contents)
		if err != nil {
			return fmt.Errorf("failed to create document: %w", err)
		}

		documents = append(documents, document)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk fs: %w", err)
	}

	return documents, nil
}
