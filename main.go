package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/madeddie/opds-aggregator/cache"
	"github.com/madeddie/opds-aggregator/config"
	"github.com/madeddie/opds-aggregator/crawler"
	"github.com/madeddie/opds-aggregator/search"
	"github.com/madeddie/opds-aggregator/server"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml (default: auto-detect)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Find config file.
	cfgPath := *configPath
	if cfgPath == "" {
		cfgPath = config.FindConfig()
		if cfgPath == "" {
			logger.Error("no config file found; use --config flag or create config.yaml")
			os.Exit(1)
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Info("config loaded", "path", cfgPath, "feeds", len(cfg.Feeds))

	// Initialize components.
	httpClient := &http.Client{Timeout: 60 * time.Second}
	crawl := crawler.New(httpClient, logger)
	feedCache := cache.NewFeedCache(logger)

	dlCache, err := cache.NewDownloadCache(cfg.Download.CacheDir, cfg.Download.CacheEnabled, logger)
	if err != nil {
		logger.Error("failed to initialize download cache", "error", err)
		os.Exit(1)
	}

	searcher := search.New(cfg, feedCache, crawl, logger)

	// Build refresh function.
	refreshFunc := func(ctx context.Context, slug string) error {
		return refreshFeeds(ctx, cfg, crawl, feedCache, logger, slug)
	}

	// Create HTTP server.
	srv := server.New(cfg, feedCache, dlCache, crawl, searcher, logger)

	// Set the refresh function on the handler (need to get it through the server).
	// We'll do initial poll, then set up the ticker.

	// Initial crawl of all feeds.
	logger.Info("performing initial feed crawl...")
	if err := refreshFunc(context.Background(), ""); err != nil {
		logger.Warn("initial crawl had errors", "error", err)
	}

	// Set up periodic polling.
	pollInterval, err := cfg.Polling.ParsedInterval()
	if err != nil {
		logger.Error("invalid polling interval", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				logger.Info("periodic feed refresh starting")
				if err := refreshFunc(ctx, ""); err != nil {
					logger.Warn("periodic refresh had errors", "error", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start HTTP server.
	go func() {
		logger.Info("starting server", "addr", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutting down", "signal", sig)

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
	logger.Info("server stopped")
}

// refreshFeeds crawls one or all feeds. If slug is empty, all feeds are refreshed.
func refreshFeeds(ctx context.Context, cfg *config.Config, crawl *crawler.Crawler, feedCache *cache.FeedCache, logger *slog.Logger, slug string) error {
	var errs []error
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, feedCfg := range cfg.Feeds {
		if slug != "" && feedCfg.Slug() != slug {
			continue
		}

		wg.Add(1)
		go func(fc config.FeedConfig) {
			defer wg.Done()
			tree, err := crawl.Crawl(ctx, fc)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", fc.Name, err))
				mu.Unlock()
				return
			}
			feedCache.Put(fc.Slug(), tree)
		}(feedCfg)
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("refresh errors: %v", errs)
	}
	return nil
}
