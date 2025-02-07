package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"testing/fstest"
)

func createTestTar(t *testing.T) (string, func()) {
	t.Helper()

	tmpfile, err := ioutil.TempFile(os.TempDir(), "thoughts-agent-test-")
	if err != nil {
		t.Fatal(err)
	}

	fileFS := fstest.MapFS{
		"README.md": &fstest.MapFile{
			Data: []byte("Hello, World!"),
		},
		"thoughts/2022-01-01.md": &fstest.MapFile{
			Data: []byte("Hello, 2022-01-01!"),
		},
	}

	zw := zip.NewWriter(tmpfile)
	zw.AddFS(fileFS)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return tmpfile.Name(), func() {
		os.Remove(tmpfile.Name())
	}
}

func TestGithubClientContents(t *testing.T) {
	tarfile, cleanup := createTestTar(t)
	defer cleanup()

	fmt.Println(tarfile)
	tarsvr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("serving tar")
		http.ServeFile(w, r, tarfile)
	}))

	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("redirecting")
		http.Redirect(w, r, tarsvr.URL, http.StatusFound)
	}))

	ghclient, err := NewGitHubClient(svr.URL, "https://github.com/josebalius/thoughts")
	if err != nil {
		t.Fatal(err)
	}

	contents, cleanup, err := ghclient.Contents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	fmt.Println(contents)
}
