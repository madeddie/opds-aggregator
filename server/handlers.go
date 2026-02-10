package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/madeddie/opds-aggregator/cache"
	"github.com/madeddie/opds-aggregator/config"
	"github.com/madeddie/opds-aggregator/crawler"
	"github.com/madeddie/opds-aggregator/opds"
	"github.com/madeddie/opds-aggregator/search"
)

// Handler holds all dependencies for the HTTP handlers.
type Handler struct {
	cfg       *config.Config
	feedCache *cache.FeedCache
	crawler   *crawler.Crawler
	searcher  *search.Searcher
	logger    *slog.Logger

	// feedMap maps slug → FeedConfig for quick lookup.
	feedMap map[string]config.FeedConfig

	// RefreshFunc is called to trigger a feed refresh. Set by the poller.
	RefreshFunc func(ctx context.Context, slug string) error
}

// NewHandler creates a new Handler.
func NewHandler(
	cfg *config.Config,
	feedCache *cache.FeedCache,
	crawl *crawler.Crawler,
	searcher *search.Searcher,
	logger *slog.Logger,
) *Handler {
	fm := make(map[string]config.FeedConfig, len(cfg.Feeds))
	for _, f := range cfg.Feeds {
		fm[f.Slug()] = f
	}
	return &Handler{
		cfg:       cfg,
		feedCache: feedCache,
		crawler:   crawl,
		searcher:  searcher,
		logger:    logger,
		feedMap:   fm,
	}
}

// HandleRoot serves the aggregator's navigation root — one entry per source feed.
func (h *Handler) HandleRoot(w http.ResponseWriter, r *http.Request) {
	h.logger.Debug("HandleRoot request",
		"method", r.Method,
		"path", r.URL.Path,
		"remoteAddr", r.RemoteAddr,
		"userAgent", r.UserAgent(),
	)
	now := time.Now().UTC().Format(time.RFC3339)
	feed := &opds.Feed{
		ID:      "urn:opds-aggregator:root",
		Title:   h.cfg.Server.Title,
		Updated: now,
		Links: []opds.Link{
			{Rel: opds.RelSelf, Href: "/opds", Type: opds.MediaTypeAtom},
			{Rel: opds.RelStart, Href: "/opds", Type: opds.MediaTypeAtom},
		},
	}

	// Add global search link if any source has search.
	feed.Links = append(feed.Links, opds.Link{
		Rel:  opds.RelSearch,
		Href: "/opds/search?q={searchTerms}",
		Type: opds.MediaTypeAtom,
	})

	for _, fc := range h.cfg.Feeds {
		slug := fc.Slug()
		updated := now
		if cached, ok := h.feedCache.Get(slug); ok {
			updated = cached.UpdatedAt.UTC().Format(time.RFC3339)
		}
		entry := opds.Entry{
			ID:      "urn:opds-aggregator:source:" + slug,
			Title:   fc.Name,
			Updated: updated,
			Content: &opds.Text{Type: "text", Body: fc.URL},
			Links: []opds.Link{
				{
					Rel:  opds.RelSubsection,
					Href: "/opds/source/" + slug + "/",
					Type: opds.MediaTypeAtom,
				},
			},
		}
		feed.Entries = append(feed.Entries, entry)
	}

	writeOPDS(w, feed, h.logger)
}

// HandleSource serves a cached or on-demand upstream feed with rewritten links.
func (h *Handler) HandleSource(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	feedCfg, ok := h.feedMap[slug]
	if !ok {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}

	// The remainder of the path after /opds/source/{slug}/
	subPath := chi.URLParam(r, "*")
	rawQuery := r.URL.RawQuery
	h.logger.Debug("HandleSource request", "slug", slug, "subPath", subPath, "rawQuery", rawQuery, "fullPath", r.URL.Path)

	cached, hasCached := h.feedCache.Get(slug)
	if !hasCached {
		// No cache yet — try on-demand fetch.
		h.logger.Info("on-demand fetch", "slug", slug)
		tree, err := h.crawler.Crawl(r.Context(), feedCfg)
		if err != nil {
			h.logger.Error("on-demand crawl failed", "slug", slug, "error", err)
			http.Error(w, "failed to fetch upstream feed", http.StatusBadGateway)
			return
		}
		h.feedCache.Put(slug, tree)
		cached = &cache.CachedFeed{Tree: tree, UpdatedAt: time.Now()}
	}

	// Determine which feed in the tree to serve.
	feed := h.resolveFeed(r.Context(), cached.Tree, feedCfg, subPath, rawQuery)
	if feed == nil {
		http.Error(w, "feed not found", http.StatusNotFound)
		return
	}

	// Apply server-side pagination BEFORE link rewriting.
	// This limits the number of entries we process and return.
	maxEntries := h.getMaxEntries(feedCfg)
	if maxEntries > 0 && len(feed.Entries) > 0 {
		offset, limit := h.parsePaginationParams(r, maxEntries)
		basePath := "/opds/source/" + slug + "/" + subPath
		feed = h.paginateFeed(feed, basePath, rawQuery, offset, limit)
	}

	// Determine the upstream base URL for this sub-feed.
	baseURL := cached.Tree.URL
	trimmedSub := strings.TrimPrefix(strings.TrimSuffix(subPath, "/"), "/")
	if trimmedSub == "ext" {
		// External URL — use it as the base for link resolution.
		if qv, err := url.ParseQuery(rawQuery); err == nil {
			if extURL := qv.Get("url"); extURL != "" {
				baseURL = extURL
			}
		}
	} else if subPath != "" {
		baseURL = joinURL(cached.Tree.URL, subPath, "")
	}

	rewritten := rewriteFeedLinks(feed, slug, baseURL, cached.Tree.URL, "")
	writeOPDS(w, rewritten, h.logger)
}

// HandleDownload proxies a download request, optionally caching it.
func (h *Handler) HandleDownload(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	feedCfg, ok := h.feedMap[slug]
	if !ok {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}

	dlURL := r.URL.Query().Get("url")
	if dlURL == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}

	// Validate the URL to prevent SSRF.
	parsed, err := url.Parse(dlURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}

	// Fetch from upstream.
	body, contentType, contentLength, err := h.crawler.FetchRaw(r.Context(), dlURL, feedCfg.Auth)
	if err != nil {
		h.logger.Error("download fetch failed", "url", dlURL, "error", err)
		http.Error(w, "failed to fetch download", http.StatusBadGateway)
		return
	}
	defer body.Close()

	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if contentLength > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", contentLength))
	}

	io.Copy(w, body)
}

// HandleSearch handles search queries across all or a specific source.
func (h *Handler) HandleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "missing q parameter", http.StatusBadRequest)
		return
	}

	if h.searcher == nil {
		http.Error(w, "search not available", http.StatusNotImplemented)
		return
	}

	results, err := h.searcher.Search(r.Context(), query)
	if err != nil {
		h.logger.Error("search failed", "query", query, "error", err)
		http.Error(w, "search failed", http.StatusBadGateway)
		return
	}

	writeOPDS(w, results, h.logger)
}

// HandleSourceSearch handles search within a specific source.
// When called without a "q" parameter but with "upstream", it serves an
// OpenSearch description document so that OPDS readers can discover the
// search endpoint. This avoids returning 400 when readers auto-fetch the
// search link to discover search capabilities.
func (h *Handler) HandleSourceSearch(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	feedCfg, ok := h.feedMap[slug]
	if !ok {
		http.Error(w, "unknown source", http.StatusNotFound)
		return
	}

	query := r.URL.Query().Get("q")
	upstreamSearch := r.URL.Query().Get("upstream")

	if upstreamSearch == "" {
		http.Error(w, "missing upstream parameter", http.StatusBadRequest)
		return
	}

	// No query yet — the reader is fetching the search description.
	// Serve a generated OpenSearch description pointing back to this endpoint.
	if query == "" {
		w.Header().Set("Content-Type", "application/opensearchdescription+xml; charset=utf-8")
		tmpl := "/opds/search/" + slug + "?upstream=" + url.QueryEscape(upstreamSearch) + "&amp;q={searchTerms}"
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>%s</ShortName>
  <Description>Search %s</Description>
  <Url type="application/atom+xml;profile=opds-catalog" template="%s"/>
</OpenSearchDescription>`, feedCfg.Name, feedCfg.Name, tmpl)
		return
	}

	if h.searcher == nil {
		http.Error(w, "search not available", http.StatusNotImplemented)
		return
	}

	results, err := h.searcher.SearchSource(r.Context(), slug, feedCfg, upstreamSearch, query)
	if err != nil {
		h.logger.Error("source search failed", "slug", slug, "query", query, "error", err)
		http.Error(w, "search failed", http.StatusBadGateway)
		return
	}

	rewritten := rewriteFeedLinks(results, slug, feedCfg.URL, feedCfg.URL, "")
	writeOPDS(w, rewritten, h.logger)
}

// HandleRefresh triggers a manual re-poll of all or a specific source.
func (h *Handler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if h.RefreshFunc == nil {
		http.Error(w, "refresh not available", http.StatusNotImplemented)
		return
	}
	if err := h.RefreshFunc(r.Context(), slug); err != nil {
		h.logger.Error("refresh failed", "slug", slug, "error", err)
		http.Error(w, "refresh failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// HandleRefreshAll triggers a manual re-poll of all sources.
func (h *Handler) HandleRefreshAll(w http.ResponseWriter, r *http.Request) {
	if h.RefreshFunc == nil {
		http.Error(w, "refresh not available", http.StatusNotImplemented)
		return
	}
	if err := h.RefreshFunc(r.Context(), ""); err != nil {
		h.logger.Error("refresh all failed", "error", err)
		http.Error(w, "refresh failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// resolveFeed finds the right feed to serve from the cached tree, given the sub-path.
func (h *Handler) resolveFeed(ctx context.Context, tree *crawler.FeedTree, feedCfg config.FeedConfig, subPath, rawQuery string) *opds.Feed {
	subPath = strings.TrimPrefix(subPath, "/")
	subPath = strings.TrimSuffix(subPath, "/")

	// Strip pagination params from the query for cache key and upstream URL.
	// Our pagination params are "offset" and "limit" — they control local slicing,
	// not upstream fetching.
	cleanQuery := stripPaginationParams(rawQuery)

	// Root of this source.
	if subPath == "" && cleanQuery == "" {
		return tree.Feed
	}

	// Build a cache key that includes query params (without pagination).
	cacheKey := subPath
	if cleanQuery != "" {
		cacheKey += "?" + cleanQuery
	}

	// Check if we have this child in the cached tree.
	if child, ok := tree.Children[cacheKey]; ok {
		return child.Feed
	}

	// Handle external URL references (produced by makeRelativePath for
	// cross-host links or links outside the source root path).
	if subPath == "ext" {
		if qv, err := url.ParseQuery(cleanQuery); err == nil {
			if extURL := qv.Get("url"); extURL != "" {
				cacheKey = "ext?url=" + url.QueryEscape(extURL)
				if child, ok := tree.Children[cacheKey]; ok {
					return child.Feed
				}
				h.logger.Info("on-demand ext fetch", "url", extURL)
				feed, err := h.fetchWithPaginationLimit(ctx, extURL, feedCfg)
				if err != nil {
					h.logger.Error("on-demand ext fetch failed", "url", extURL, "error", err)
					return nil
				}
				tree.Children[cacheKey] = &crawler.FeedTree{
					Feed:     feed,
					URL:      extURL,
					Children: make(map[string]*crawler.FeedTree),
				}
				return feed
			}
		}
	}

	// Not in cache — fetch on demand from upstream.
	upstreamURL := joinURL(tree.URL, subPath, cleanQuery)
	h.logger.Info("on-demand sub-feed fetch", "url", upstreamURL)

	feed, err := h.fetchWithPaginationLimit(ctx, upstreamURL, feedCfg)
	if err != nil {
		h.logger.Error("on-demand fetch failed", "url", upstreamURL, "error", err)
		return nil
	}

	// Cache the result for future requests.
	tree.Children[cacheKey] = &crawler.FeedTree{
		Feed:     feed,
		URL:      upstreamURL,
		Children: make(map[string]*crawler.FeedTree),
	}

	return feed
}

// fetchWithPaginationLimit fetches a feed using the feed's max_paginate setting.
func (h *Handler) fetchWithPaginationLimit(ctx context.Context, feedURL string, feedCfg config.FeedConfig) (*opds.Feed, error) {
	maxPages := feedCfg.MaxPaginate
	if maxPages == 0 {
		// No limit configured — fetch all pages.
		return h.crawler.FetchPaginated(ctx, feedURL, feedCfg.Auth)
	}
	feed, _, _, err := h.crawler.FetchWithLimit(ctx, feedURL, feedCfg.Auth, maxPages)
	return feed, err
}

// stripPaginationParams removes offset and limit query params.
func stripPaginationParams(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return rawQuery
	}
	values.Del("offset")
	values.Del("limit")
	return values.Encode()
}

// getMaxEntries returns the max entries per page for a feed.
// Uses feed-specific setting if configured, otherwise server default.
func (h *Handler) getMaxEntries(feedCfg config.FeedConfig) int {
	if feedCfg.MaxEntries > 0 {
		return feedCfg.MaxEntries
	}
	return h.cfg.Server.DefaultMaxEntries
}

// parsePaginationParams extracts offset and limit from query params.
func (h *Handler) parsePaginationParams(r *http.Request, defaultLimit int) (offset, limit int) {
	limit = defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return offset, limit
}

// paginateFeed slices the feed entries and generates pagination links.
func (h *Handler) paginateFeed(feed *opds.Feed, basePath, rawQuery string, offset, limit int) *opds.Feed {
	totalEntries := len(feed.Entries)
	if totalEntries == 0 {
		return feed
	}

	// Calculate bounds.
	start := offset
	if start > totalEntries {
		start = totalEntries
	}
	end := start + limit
	if end > totalEntries {
		end = totalEntries
	}

	// Create a new feed with sliced entries.
	out := *feed
	out.Entries = feed.Entries[start:end]

	// Set OpenSearch pagination elements.
	out.TotalResults = totalEntries
	out.ItemsPerPage = limit
	out.StartIndex = start + 1 // OpenSearch uses 1-based indexing

	// Build pagination links.
	out.Links = h.addPaginationLinks(feed.Links, basePath, rawQuery, offset, limit, totalEntries)

	return &out
}

// addPaginationLinks generates first/previous/next/last pagination links.
func (h *Handler) addPaginationLinks(existingLinks []opds.Link, basePath, rawQuery string, offset, limit, total int) []opds.Link {
	// Copy existing links, excluding any existing pagination links.
	links := make([]opds.Link, 0, len(existingLinks)+4)
	for _, l := range existingLinks {
		if l.Rel != opds.RelFirst && l.Rel != opds.RelPrevious &&
			l.Rel != opds.RelNext && l.Rel != opds.RelLast {
			links = append(links, l)
		}
	}

	// Helper to build pagination URL.
	makeURL := func(newOffset int) string {
		return buildPaginationURL(basePath, rawQuery, newOffset, limit)
	}

	// First link (always present for consistency).
	links = append(links, opds.Link{
		Rel:  opds.RelFirst,
		Href: makeURL(0),
		Type: opds.MediaTypeAtom,
	})

	// Previous link.
	if offset > 0 {
		prevOffset := offset - limit
		if prevOffset < 0 {
			prevOffset = 0
		}
		links = append(links, opds.Link{
			Rel:  opds.RelPrevious,
			Href: makeURL(prevOffset),
			Type: opds.MediaTypeAtom,
		})
	}

	// Next link.
	if offset+limit < total {
		links = append(links, opds.Link{
			Rel:  opds.RelNext,
			Href: makeURL(offset + limit),
			Type: opds.MediaTypeAtom,
		})
	}

	// Last link.
	lastOffset := ((total - 1) / limit) * limit
	if lastOffset < 0 {
		lastOffset = 0
	}
	links = append(links, opds.Link{
		Rel:  opds.RelLast,
		Href: makeURL(lastOffset),
		Type: opds.MediaTypeAtom,
	})

	return links
}

// buildPaginationURL constructs a URL with pagination params.
func buildPaginationURL(basePath, rawQuery string, offset, limit int) string {
	values, _ := url.ParseQuery(rawQuery)
	// Remove existing pagination params.
	values.Del("offset")
	values.Del("limit")
	// Add new pagination params.
	if offset > 0 {
		values.Set("offset", strconv.Itoa(offset))
	}
	values.Set("limit", strconv.Itoa(limit))

	if len(values) > 0 {
		return basePath + "?" + values.Encode()
	}
	return basePath
}

func writeOPDS(w http.ResponseWriter, feed *opds.Feed, logger *slog.Logger) {
	w.Header().Set("Content-Type", opds.MediaTypeAtom+"; charset=utf-8")
	if err := opds.Render(w, feed); err != nil {
		logger.Error("failed to write OPDS response", "error", err)
	}
}

