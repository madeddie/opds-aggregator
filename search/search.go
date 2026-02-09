// Package search provides fan-out search across upstream OPDS catalogs.
package search

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/madeddie/opds-aggregator/cache"
	"github.com/madeddie/opds-aggregator/config"
	"github.com/madeddie/opds-aggregator/crawler"
	"github.com/madeddie/opds-aggregator/opds"
)

// OpenSearchDescription is a minimal representation of an OpenSearch description document.
type OpenSearchDescription struct {
	XMLName     xml.Name `xml:"OpenSearchDescription"`
	ShortName   string   `xml:"ShortName"`
	Description string   `xml:"Description"`
	URLTemplate string   `xml:"-"` // extracted from <Url> element
}

// OpenSearchURL represents a <Url> element in OpenSearch.
type OpenSearchURL struct {
	Template string `xml:"template,attr"`
	Type     string `xml:"type,attr"`
}

// Searcher handles search requests across upstream feeds.
type Searcher struct {
	cfg       *config.Config
	feedCache *cache.FeedCache
	crawler   *crawler.Crawler
	logger    *slog.Logger
}

// New creates a new Searcher.
func New(cfg *config.Config, feedCache *cache.FeedCache, crawl *crawler.Crawler, logger *slog.Logger) *Searcher {
	return &Searcher{
		cfg:       cfg,
		feedCache: feedCache,
		crawler:   crawl,
		logger:    logger,
	}
}

// Search performs a fan-out search across all sources that have OpenSearch endpoints.
func (s *Searcher) Search(ctx context.Context, query string) (*opds.Feed, error) {
	results := &opds.Feed{
		ID:      "urn:opds-aggregator:search:" + url.QueryEscape(query),
		Title:   fmt.Sprintf("Search results for %q", query),
		Updated: time.Now().UTC().Format(time.RFC3339),
		Links: []opds.Link{
			{Rel: opds.RelSelf, Href: "/opds/search?q=" + url.QueryEscape(query), Type: opds.MediaTypeAtom},
			{Rel: opds.RelStart, Href: "/opds", Type: opds.MediaTypeAtom},
		},
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, feedCfg := range s.cfg.Feeds {
		cached, ok := s.feedCache.Get(feedCfg.Slug())
		if !ok || cached.Tree.SearchURL == "" {
			continue
		}

		wg.Add(1)
		go func(fc config.FeedConfig, searchURL string) {
			defer wg.Done()

			entries, err := s.searchUpstream(ctx, fc, searchURL, query)
			if err != nil {
				s.logger.Warn("search failed for source", "name", fc.Name, "error", err)
				return
			}

			mu.Lock()
			results.Entries = append(results.Entries, entries...)
			mu.Unlock()
		}(feedCfg, cached.Tree.SearchURL)
	}

	wg.Wait()
	return results, nil
}

// SearchSource searches a specific upstream source.
func (s *Searcher) SearchSource(ctx context.Context, slug string, feedCfg config.FeedConfig, searchDescURL, query string) (*opds.Feed, error) {
	// Fetch the OpenSearch description to get the URL template.
	tmpl, err := s.fetchSearchTemplate(ctx, searchDescURL, feedCfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("fetch search template: %w", err)
	}

	searchURL := expandTemplate(tmpl, query)
	feed, err := s.crawler.FetchPaginated(ctx, searchURL, feedCfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("search source %s: %w", slug, err)
	}

	return feed, nil
}

func (s *Searcher) searchUpstream(ctx context.Context, feedCfg config.FeedConfig, searchDescURL, query string) ([]opds.Entry, error) {
	tmpl, err := s.fetchSearchTemplate(ctx, searchDescURL, feedCfg.Auth)
	if err != nil {
		return nil, err
	}

	searchURL := expandTemplate(tmpl, query)
	feed, err := s.crawler.FetchFeedByURL(ctx, searchURL, feedCfg.Auth)
	if err != nil {
		return nil, err
	}

	// Tag entries with their source.
	for i := range feed.Entries {
		feed.Entries[i].Categories = append(feed.Entries[i].Categories, opds.Category{
			Term:   feedCfg.Slug(),
			Label:  feedCfg.Name,
			Scheme: "urn:opds-aggregator:source",
		})
	}

	return feed.Entries, nil
}

func (s *Searcher) fetchSearchTemplate(ctx context.Context, descURL string, auth *config.AuthConfig) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, descURL, nil)
	if err != nil {
		return "", err
	}
	if auth != nil && auth.Username != "" {
		req.SetBasicAuth(auth.Username, auth.Password)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenSearch desc HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}

	// Parse the OpenSearch description XML to extract the URL template.
	// The <Url> element has a template attribute.
	type osURL struct {
		Template string `xml:"template,attr"`
		Type     string `xml:"type,attr"`
	}
	type osDesc struct {
		XMLName xml.Name `xml:"OpenSearchDescription"`
		URLs    []osURL  `xml:"Url"`
	}
	var desc osDesc
	if err := xml.Unmarshal(body, &desc); err != nil {
		return "", fmt.Errorf("parse OpenSearch desc: %w", err)
	}

	// Prefer OPDS Atom results.
	for _, u := range desc.URLs {
		if strings.Contains(u.Type, "atom") {
			return u.Template, nil
		}
	}
	// Fallback to first URL.
	if len(desc.URLs) > 0 {
		return desc.URLs[0].Template, nil
	}

	return "", fmt.Errorf("no URL template in OpenSearch description")
}

// expandTemplate replaces {searchTerms} in an OpenSearch URL template.
func expandTemplate(tmpl, query string) string {
	result := strings.ReplaceAll(tmpl, "{searchTerms}", url.QueryEscape(query))
	// Remove other optional OpenSearch parameters.
	result = strings.ReplaceAll(result, "{startPage?}", "")
	result = strings.ReplaceAll(result, "{startIndex?}", "")
	result = strings.ReplaceAll(result, "{count?}", "")
	result = strings.ReplaceAll(result, "{language?}", "")
	result = strings.ReplaceAll(result, "{inputEncoding?}", "")
	result = strings.ReplaceAll(result, "{outputEncoding?}", "")
	return result
}
