// Package server provides the HTTP server for the OPDS aggregator.
package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/madeddie/opds-aggregator/cache"
	"github.com/madeddie/opds-aggregator/config"
	"github.com/madeddie/opds-aggregator/crawler"
	"github.com/madeddie/opds-aggregator/search"
)

// New creates a configured HTTP server with all routes.
func New(
	cfg *config.Config,
	feedCache *cache.FeedCache,
	dlCache *cache.DownloadCache,
	crawl *crawler.Crawler,
	searcher *search.Searcher,
	logger *slog.Logger,
) *http.Server {
	h := NewHandler(cfg, feedCache, dlCache, crawl, searcher, logger)

	r := chi.NewRouter()
	r.Use(chimiddleware.Recoverer)
	r.Use(chimiddleware.RealIP)
	r.Use(RequestLogger(logger))
	r.Use(BasicAuth(cfg.Server.Auth))

	// OPDS routes.
	r.Get("/opds", h.HandleRoot)
	r.Get("/opds/", h.HandleRoot)
	r.Get("/opds/source/{slug}/*", h.HandleSource)
	r.Get("/opds/download/{slug}", h.HandleDownload)
	r.Get("/opds/search", h.HandleSearch)
	r.Get("/opds/search/{slug}", h.HandleSourceSearch)

	// Management routes.
	r.Post("/opds/refresh", h.HandleRefreshAll)
	r.Post("/opds/refresh/{slug}", h.HandleRefresh)

	return &http.Server{
		Addr:    cfg.Server.Addr,
		Handler: r,
	}
}
