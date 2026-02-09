# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OPDS Aggregator is a single-binary Go server that combines multiple OPDS 1.2 (Open Publication Distribution System) catalogs into a unified feed for e-readers. It proxies book downloads and enables cross-catalog searching.

## Build Commands

```bash
# Build
go build -o opds-aggregator .

# Run with debug logging
./opds-aggregator --config config.yaml --debug

# Run (uses XDG config path by default)
./opds-aggregator
```

No test suite or linting tools are configured. QA relies on manual testing with real OPDS sources.

## Architecture

**Data Flow:**
```
Config YAML → Config loader → Feed Crawler → Feed Cache → HTTP Server
```

**Core Components:**
- `config/` - YAML parsing, validation, XDG config discovery
- `crawler/` - HTTP fetching, OPDS/Atom parsing, depth-based recursive navigation
- `opds/` - Data structures for Atom/OPDS 1.2, parsing, rendering to XML
- `cache/` - FeedCache (in-memory, thread-safe), DownloadCache (disk-backed with SHA256)
- `search/` - Fan-out search via OpenSearch description documents
- `server/` - HTTP routing (chi), handlers, Basic Auth middleware, link rewriting

**URL Rewriting Strategy** (server/rewrite.go):
- Navigation links → `/opds/source/{slug}/...`
- Acquisition/image links → `/opds/download/{slug}?url=...`
- Search links → `/opds/search/{slug}?upstream=...`
- Cross-host links → `ext?url=...` (for sources with non-matching path prefixes)

**Feed Caching:**
- FeedTree structure: root feed + map of children keyed by relative path
- `poll_depth` controls crawl depth (0 = on-demand fetching)
- Thread-safe with RWMutex for concurrent access

## Key Patterns

- **Dependency Injection** - components accept dependencies as constructor args
- **Error Wrapping** - always wrap errors with context using `fmt.Errorf` + `%w`
- **Structured Logging** - use `slog` with key-value pairs
- **Context Propagation** - all HTTP operations accept and pass `context.Context`
- **URL Handling** - use `url.Parse()` + `url.ResolveReference()` for safety

## Known Edge Cases

Based on recent fixes, be aware of:
1. **Path prefix mismatches** - source roots may have different path prefixes than navigation links
2. **Query strings in base URLs** - must be stripped before relative path joining
3. **Trailing slashes** - normalized to be "directory-like" for proper relative URL resolution
4. **Large catalogs** - Gutenberg's 70k+ entries require on-demand fetching (poll_depth: 0)
