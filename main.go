package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
)

var (
	repoURL  = flag.String("repo", "", "the repo to use")
	useCache = flag.Bool("use-cache", false, "use the cache, if true, it creates the cache and uses it if it exists")
)

func main() {
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	logger := log.New(os.Stderr, "", log.LstdFlags)

	if err := run(ctx, logger, *repoURL, *useCache); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *log.Logger, repoURL string, useCache bool) error {
	if repoURL == "" {
		return fmt.Errorf("repo url is required")
	}

	site, err := newSite(logger, repoURL, useCache)
	if err != nil {
		return fmt.Errorf("failed to create site: %w", err)
	}

	return site.Serve(ctx)
}
