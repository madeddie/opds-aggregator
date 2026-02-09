// Package cache provides thread-safe in-memory feed caching.
package cache

import (
	"log/slog"
	"sync"
	"time"

	"github.com/madeddie/opds-aggregator/crawler"
)

// FeedCache stores crawled feed trees in memory.
type FeedCache struct {
	mu      sync.RWMutex
	entries map[string]*CachedFeed // keyed by source slug
	logger  *slog.Logger
}

// CachedFeed is a single cached source.
type CachedFeed struct {
	Tree      *crawler.FeedTree
	UpdatedAt time.Time
}

// NewFeedCache creates a new empty feed cache.
func NewFeedCache(logger *slog.Logger) *FeedCache {
	if logger == nil {
		logger = slog.Default()
	}
	return &FeedCache{
		entries: make(map[string]*CachedFeed),
		logger:  logger,
	}
}

// Put stores a feed tree under the given slug.
func (fc *FeedCache) Put(slug string, tree *crawler.FeedTree) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.entries[slug] = &CachedFeed{
		Tree:      tree,
		UpdatedAt: time.Now(),
	}
	fc.logger.Info("feed cached", "slug", slug)
}

// Get retrieves the cached feed tree for a slug.
func (fc *FeedCache) Get(slug string) (*CachedFeed, bool) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	entry, ok := fc.entries[slug]
	return entry, ok
}

// All returns all cached feeds.
func (fc *FeedCache) All() map[string]*CachedFeed {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	result := make(map[string]*CachedFeed, len(fc.entries))
	for k, v := range fc.entries {
		result[k] = v
	}
	return result
}

// Remove deletes a cached feed.
func (fc *FeedCache) Remove(slug string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	delete(fc.entries, slug)
}
