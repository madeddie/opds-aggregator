// Package cache provides thread-safe in-memory feed caching and disk-based download caching.
package cache

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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

// DownloadCache provides disk-backed caching for proxied downloads.
type DownloadCache struct {
	dir     string
	enabled bool
	logger  *slog.Logger
}

// NewDownloadCache creates a new download cache. If disabled, all operations are no-ops.
func NewDownloadCache(dir string, enabled bool, logger *slog.Logger) (*DownloadCache, error) {
	if logger == nil {
		logger = slog.Default()
	}
	dc := &DownloadCache{
		dir:     dir,
		enabled: enabled,
		logger:  logger,
	}
	if enabled {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("download cache: create dir %s: %w", dir, err)
		}
	}
	return dc, nil
}

// CacheKey produces a deterministic filename for a download URL.
func CacheKey(downloadURL string) string {
	h := sha256.Sum256([]byte(downloadURL))
	return fmt.Sprintf("%x", h)
}

// Has returns true if the download is cached on disk.
func (dc *DownloadCache) Has(downloadURL string) bool {
	if !dc.enabled {
		return false
	}
	key := CacheKey(downloadURL)
	_, err := os.Stat(dc.filePath(key))
	return err == nil
}

// Get opens the cached file for reading. Returns nil if not cached.
func (dc *DownloadCache) Get(downloadURL string) (*os.File, *CachedDownloadMeta, error) {
	if !dc.enabled {
		return nil, nil, nil
	}
	key := CacheKey(downloadURL)
	f, err := os.Open(dc.filePath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	meta, err := dc.readMeta(key)
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, meta, nil
}

// Put writes a download to the cache, reading from r.
func (dc *DownloadCache) Put(downloadURL string, contentType string, r io.Reader) error {
	if !dc.enabled {
		return nil
	}
	key := CacheKey(downloadURL)

	f, err := os.Create(dc.filePath(key))
	if err != nil {
		return fmt.Errorf("download cache: create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		os.Remove(dc.filePath(key))
		return fmt.Errorf("download cache: write file: %w", err)
	}

	// Write metadata alongside.
	if err := dc.writeMeta(key, &CachedDownloadMeta{
		URL:         downloadURL,
		ContentType: contentType,
		CachedAt:    time.Now(),
	}); err != nil {
		dc.logger.Warn("failed to write download meta", "key", key, "error", err)
	}

	dc.logger.Debug("download cached", "url", downloadURL, "key", key)
	return nil
}

func (dc *DownloadCache) filePath(key string) string {
	// Use first 2 chars as subdirectory to avoid huge flat dirs.
	return filepath.Join(dc.dir, key[:2], key)
}

// CachedDownloadMeta stores metadata about a cached download.
type CachedDownloadMeta struct {
	URL         string
	ContentType string
	CachedAt    time.Time
}

func (dc *DownloadCache) metaPath(key string) string {
	return dc.filePath(key) + ".meta"
}

func (dc *DownloadCache) writeMeta(key string, meta *CachedDownloadMeta) error {
	dir := filepath.Dir(dc.filePath(key))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data := fmt.Sprintf("%s\n%s\n%s\n", meta.URL, meta.ContentType, meta.CachedAt.Format(time.RFC3339))
	return os.WriteFile(dc.metaPath(key), []byte(data), 0o644)
}

func (dc *DownloadCache) readMeta(key string) (*CachedDownloadMeta, error) {
	data, err := os.ReadFile(dc.metaPath(key))
	if err != nil {
		return nil, err
	}
	lines := splitLines(string(data))
	if len(lines) < 3 {
		return nil, fmt.Errorf("download cache: corrupt meta for %s", key)
	}
	cachedAt, _ := time.Parse(time.RFC3339, lines[2])
	return &CachedDownloadMeta{
		URL:         lines[0],
		ContentType: lines[1],
		CachedAt:    cachedAt,
	}, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
