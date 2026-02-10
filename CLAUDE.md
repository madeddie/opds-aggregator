# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OPDS Aggregator is a single-binary Go server that combines multiple OPDS 1.2 (Open Publication Distribution System) catalogs into a unified feed for e-readers. It proxies book downloads and enables cross-catalog searching via OpenSearch.

## Build & Run

```bash
# Build
go build -o opds-aggregator .

# Run with debug logging
./opds-aggregator --config config.yaml --debug

# Run (auto-detects config at ./config.yaml or XDG paths)
./opds-aggregator
```

No test suite or linting tools are configured. QA relies on manual testing with real OPDS sources.

## CI/CD

GitHub Actions (`.github/workflows/build.yml`) runs on push/PR to main:
- Builds multi-platform binaries (linux/darwin x amd64/arm64)
- Auto-creates GitHub releases on main push (tag: `v{YYYYMMDD}-{commit:7}`)
- Go version is read from `go.mod`

## Repository Structure

```
main.go                  # Entry point: flags, startup, polling, graceful shutdown
config/config.go         # YAML parsing, validation, XDG config discovery
cache/cache.go           # FeedCache: in-memory, thread-safe (RWMutex)
crawler/crawler.go       # HTTP fetching, OPDS/Atom parsing, recursive crawl
opds/
  types.go               # Feed/Entry/Link structs, OPDS 1.2 constants, namespaces
  parse.go               # XML decoding
  render.go              # XML encoding with declaration
search/search.go         # Fan-out search: OpenSearch description + template expansion
server/
  server.go              # Chi router setup, middleware wiring
  handlers.go            # HTTP handlers (root, source, download, search, refresh)
  middleware.go           # Request logging, Basic Auth (constant-time compare)
  rewrite.go             # Link rewriting: navigation, acquisition, search, external
config.example.yaml      # Configuration template
```

## Architecture

**Startup flow** (main.go):
1. Parse flags (`--config`, `--debug`) and configure `slog` logger
2. Find and load YAML config (flag → `./config.yaml` → XDG paths)
3. Initialize: HTTP client (60s timeout) → Crawler → FeedCache → Searcher
4. Concurrent initial crawl of all feeds
5. Start periodic polling goroutine
6. Start HTTP server, wait for SIGINT/SIGTERM, graceful shutdown

**Data flow:**
```
Config YAML → Config Loader → Crawler → FeedCache → HTTP Server → Link Rewriter → XML Response
```

**Core components:**
- `config/` — YAML parsing, validation (unique slugs, required fields), XDG config discovery
- `crawler/` — HTTP fetching with auth, OPDS/Atom XML parsing, recursive navigation crawl up to `poll_depth`, pagination following
- `opds/` — Atom/OPDS 1.2 data structures, XML parse/render, link relation constants
- `cache/` — FeedCache: in-memory thread-safe feed tree storage (RWMutex)
- `search/` — Fan-out search across sources via OpenSearch description documents and template expansion
- `server/` — Chi HTTP routing, handlers, Basic Auth middleware, link rewriting

## HTTP Endpoints

All routes under `/opds`, protected by optional Basic Auth:

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/opds` | HandleRoot | Root catalog listing all sources |
| GET | `/opds/source/{slug}/*` | HandleSource | Browse upstream feed (rewritten links) |
| GET | `/opds/download/{slug}?url=...` | HandleDownload | Proxy acquisition/image downloads |
| GET | `/opds/search?q=...` | HandleSearch | Cross-source fan-out search |
| GET | `/opds/search/{slug}?upstream=...&q=...` | HandleSourceSearch | Per-source search (also serves OpenSearch description) |
| POST | `/opds/refresh` | HandleRefreshAll | Manual refresh of all feeds |
| POST | `/opds/refresh/{slug}` | HandleRefresh | Manual refresh of one feed |

## URL Rewriting Strategy (server/rewrite.go)

All upstream links are rewritten to route through the aggregator:
- **Navigation links** → `/opds/source/{slug}/relative-path`
- **Acquisition/image links** → `/opds/download/{slug}?url=<encoded-upstream-url>`
- **Search links** → `/opds/search/{slug}?upstream=<encoded-url>`
- **Cross-host/out-of-tree links** → `/opds/source/{slug}/ext?url=<encoded-url>`

## Feed Caching

- `FeedTree` structure: root feed + map of children keyed by relative path
- `poll_depth` controls recursive crawl depth at startup/refresh
- `poll_depth: 0` means on-demand fetching only (required for large catalogs like Gutenberg)
- On-demand fetches are cached in the tree for subsequent requests
- Thread-safe with RWMutex for concurrent reads

## Key Patterns

- **Dependency injection** — all components accept dependencies as constructor args; no global state
- **Error wrapping** — always wrap errors with context: `fmt.Errorf("context: %w", err)`
- **Structured logging** — `log/slog` with key-value pairs: `logger.Info("msg", "key", value)`
- **Context propagation** — all HTTP operations accept and pass `context.Context`
- **URL handling** — use `url.Parse()` + `url.ResolveReference()` for RFC 3986 compliance
- **Concurrency** — `sync.WaitGroup` for parallel crawl/search, `sync.RWMutex` for cache, `sync.Mutex` for error collection
- **Security** — `crypto/subtle.ConstantTimeCompare` for auth, URL scheme validation (http/https only) to prevent SSRF

## Configuration

YAML file with these sections (see `config.example.yaml`):

```yaml
server:
  addr: ":8080"              # Listen address (default: :8080)
  title: "OPDS Aggregator"   # Catalog title (default)
  auth:                       # Optional Basic Auth
    username: "reader"
    password: "changeme"

polling:
  interval: "6h"             # Go duration string (default: 6h)

feeds:
  - name: "Feed Name"        # Required: display name (→ URL slug)
    url: "https://..."        # Required: OPDS feed URL
    poll_depth: 0             # Crawl depth (0 = on-demand only)
    auth:                     # Optional per-feed Basic Auth
      username: "user"
      password: "secret"
```

**Validation:** at least one feed required, unique slugs, each feed needs name + URL.

## Dependencies

- `github.com/go-chi/chi/v5` — HTTP router and middleware
- `gopkg.in/yaml.v3` — YAML configuration parsing
- Go 1.24+ (uses `log/slog` from stdlib)

## Known Edge Cases

Based on recent fixes, be aware of:
1. **Path prefix mismatches** — source roots may have different path prefixes than navigation links
2. **Query strings in base URLs** — must be stripped before relative path joining (see `joinURL()`)
3. **Trailing slashes** — normalized to be "directory-like" for proper relative URL resolution
4. **Large catalogs** — Gutenberg's 70k+ entries require on-demand fetching (`poll_depth: 0`)
5. **Cross-host navigation** — some OPDS sources link to different hosts, handled via `ext?url=...` pattern
6. **OpenSearch description serving** — readers fetch search links without `q` param to discover capabilities; must return valid OpenSearch XML, not 400
