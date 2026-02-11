// Package crawler fetches and parses upstream OPDS catalogs.
package crawler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/madeddie/opds-aggregator/config"
	"github.com/madeddie/opds-aggregator/opds"
)

// FeedTree represents a cached upstream feed and its navigable children.
type FeedTree struct {
	Feed            *opds.Feed
	URL             string
	Children        map[string]*FeedTree // keyed by path relative to the source root
	SearchURL       string               // OpenSearch description URL, if found
	HasMoreUpstream bool                 // true if upstream has more pages available
	NextUpstreamURL string               // URL for the next upstream page (if HasMoreUpstream)
}

// Crawler fetches upstream OPDS feeds.
type Crawler struct {
	client *http.Client
	logger *slog.Logger
}

// New creates a new Crawler with the given HTTP client.
func New(client *http.Client, logger *slog.Logger) *Crawler {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Crawler{client: client, logger: logger}
}

// Crawl fetches the feed tree for a single upstream source, crawling navigation
// links up to the configured depth.
func (c *Crawler) Crawl(ctx context.Context, feedCfg config.FeedConfig) (*FeedTree, error) {
	c.logger.Info("crawling feed", "name", feedCfg.Name, "url", feedCfg.URL, "depth", feedCfg.PollDepth)

	tree := &FeedTree{
		URL:      feedCfg.URL,
		Children: make(map[string]*FeedTree),
	}

	feed, err := c.fetchFeed(ctx, feedCfg.URL, feedCfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("crawler: fetch root %s: %w", feedCfg.URL, err)
	}
	tree.Feed = feed

	// Extract search URL from root feed.
	if sl := feed.SearchLink(); sl != nil {
		tree.SearchURL = resolveURL(feedCfg.URL, sl.Href)
	}

	// Crawl navigation links recursively.
	if feedCfg.PollDepth > 0 {
		if err := c.crawlChildren(ctx, tree, feedCfg, feedCfg.URL, 1); err != nil {
			c.logger.Warn("partial crawl failure", "name", feedCfg.Name, "error", err)
		}
	}

	c.logger.Info("crawl complete", "name", feedCfg.Name, "children", len(tree.Children))
	return tree, nil
}

func (c *Crawler) crawlChildren(ctx context.Context, tree *FeedTree, feedCfg config.FeedConfig, baseURL string, depth int) error {
	if depth > feedCfg.PollDepth {
		return nil
	}
	if tree.Feed == nil {
		return nil
	}

	for _, entry := range tree.Feed.Entries {
		for _, link := range entry.Links {
			if !isNavigationLink(link) {
				continue
			}

			absURL := resolveURL(baseURL, link.Href)
			relPath := relativePath(feedCfg.URL, absURL)

			// Avoid re-crawling.
			if _, exists := tree.Children[relPath]; exists {
				continue
			}

			child, err := c.fetchFeed(ctx, absURL, feedCfg.Auth)
			if err != nil {
				c.logger.Warn("skipping child feed", "url", absURL, "error", err)
				continue
			}

			childTree := &FeedTree{
				Feed:     child,
				URL:      absURL,
				Children: make(map[string]*FeedTree),
			}
			tree.Children[relPath] = childTree

			// Recurse into navigation feeds.
			if child.IsNavigationFeed() && depth < feedCfg.PollDepth {
				if err := c.crawlChildren(ctx, childTree, feedCfg, absURL, depth+1); err != nil {
					c.logger.Warn("child crawl failed", "url", absURL, "error", err)
				}
			}
		}
	}
	return nil
}

// FetchFeedByURL fetches a single feed URL with optional auth (used for on-demand fetching).
func (c *Crawler) FetchFeedByURL(ctx context.Context, feedURL string, auth *config.AuthConfig) (*opds.Feed, error) {
	return c.fetchFeed(ctx, feedURL, auth)
}

// FetchPaginated fetches a feed and follows all "next" pagination links, merging entries.
func (c *Crawler) FetchPaginated(ctx context.Context, feedURL string, auth *config.AuthConfig) (*opds.Feed, error) {
	feed, _, _, err := c.FetchWithLimit(ctx, feedURL, auth, 0)
	return feed, err
}

// FetchResult contains the result of a paginated fetch.
type FetchResult struct {
	Feed    *opds.Feed
	HasMore bool   // true if there are more upstream pages
	NextURL string // URL for the next upstream page (if HasMore)
}

// FetchWithLimit fetches a feed and follows up to maxPages of "next" pagination links.
// If maxPages is 0, all pages are followed. Returns the merged feed, whether more
// pages exist upstream, and the URL for the next page (if any).
func (c *Crawler) FetchWithLimit(ctx context.Context, feedURL string, auth *config.AuthConfig, maxPages int) (*opds.Feed, bool, string, error) {
	feed, err := c.fetchFeed(ctx, feedURL, auth)
	if err != nil {
		return nil, false, "", err
	}

	current := feed
	pageCount := 1
	for {
		nextLink := current.NextLink()
		if nextLink == nil {
			break
		}

		nextURL := resolveURL(feedURL, nextLink.Href)

		// If we've reached the page limit, return with hasMore=true and the next URL.
		if maxPages > 0 && pageCount >= maxPages {
			return feed, true, nextURL, nil
		}

		next, err := c.fetchFeed(ctx, nextURL, auth)
		if err != nil {
			c.logger.Warn("pagination fetch failed", "url", nextURL, "error", err)
			break
		}

		feed.Entries = append(feed.Entries, next.Entries...)
		current = next
		pageCount++
	}

	return feed, false, "", nil
}

func (c *Crawler) fetchFeed(ctx context.Context, feedURL string, auth *config.AuthConfig) (*opds.Feed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", opds.MediaTypeAtom)
	if auth != nil && auth.Username != "" {
		req.SetBasicAuth(auth.Username, auth.Password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", feedURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("fetch %s: HTTP %d: %s", feedURL, resp.StatusCode, string(body))
	}

	feed, err := opds.Parse(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", feedURL, err)
	}

	return feed, nil
}

// FetchRaw fetches a URL and returns the raw response body and content type.
// Used for proxying downloads.
func (c *Crawler) FetchRaw(ctx context.Context, rawURL string, auth *config.AuthConfig) (io.ReadCloser, string, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", 0, fmt.Errorf("build request: %w", err)
	}

	if auth != nil && auth.Username != "" {
		req.SetBasicAuth(auth.Username, auth.Password)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", 0, fmt.Errorf("fetch %s: %w", rawURL, err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", 0, fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}

	return resp.Body, resp.Header.Get("Content-Type"), resp.ContentLength, nil
}

func isNavigationLink(l opds.Link) bool {
	if l.Rel != opds.RelSubsection {
		return false
	}
	return strings.Contains(l.Type, "opds-catalog") || strings.Contains(l.Type, "atom+xml")
}

func resolveURL(base, ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return ref
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	// Ensure the base path is treated as a directory so relative refs append
	// instead of replacing the last segment (e.g., /opds + "foo" → /opds/foo).
	if !strings.HasSuffix(baseURL.Path, "/") {
		baseURL.Path += "/"
	}
	return baseURL.ResolveReference(refURL).String()
}

func relativePath(base, full string) string {
	baseURL, err := url.Parse(base)
	if err != nil {
		return full
	}
	fullURL, err := url.Parse(full)
	if err != nil {
		return full
	}
	// Strip the base path prefix.
	rel := strings.TrimPrefix(fullURL.Path, baseURL.Path)
	rel = strings.TrimPrefix(rel, "/")
	if fullURL.RawQuery != "" {
		rel += "?" + fullURL.RawQuery
	}
	return rel
}
