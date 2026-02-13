# OPDS Aggregator
![vibe-coded](https://img.shields.io/badge/vibe-coded-blue)

A single-binary Go server that combines multiple OPDS 1.2 catalogs into one unified feed. Point your e-reader (KOReader, etc.) at a single URL and browse all your book sources in one place.

## Features

- **Unified catalog** — each upstream feed appears as a top-level entry, with its full structure preserved underneath
- **Download proxying** — all acquisitions (book downloads, cover images) are proxied through the aggregator
- **Basic Auth** — protect the aggregator with a username/password; per-source upstream credentials supported
- **Periodic polling** — configurable automatic refresh of upstream feeds, plus a manual refresh endpoint
- **Search** — fan-out proxy search across upstream OpenSearch endpoints, merged into a single result feed
- **On-demand fetching** — uncached sub-feeds are fetched transparently when a client navigates to them
- **Server-side pagination** — large feeds are automatically paginated to prevent hangs and reduce memory usage
- **KOReader compatible** — tested with KOReader; serves OPDS 1.2 Atom XML with proper facet passthrough

## Building

Requires Go 1.21+.

```sh
go build -o opds-aggregator .
```

This produces a single static binary.

## Configuration

Copy the example config and edit it:

```sh
cp config.example.yaml config.yaml
```

The server looks for config in this order:

1. Path passed via `--config` flag
2. `./config.yaml` or `./config.yml`
3. `~/.config/opds-aggregator/config.yaml`
4. `$XDG_CONFIG_HOME/opds-aggregator/config.yaml`

### Example config

```yaml
server:
  addr: ":8080"
  title: "My Books"
  auth:
    username: "reader"
    password: "changeme"
  default_max_entries: 100

polling:
  interval: "6h"

feeds:
  - name: "Project Gutenberg"
    url: "https://m.gutenberg.org/ebooks.opds/"
    poll_depth: 0
    max_entries: 50      # paginate large catalog
    max_paginate: 1      # only fetch one upstream page at a time

  - name: "Standard Ebooks"
    url: "https://standardebooks.org/feeds/opds"
    poll_depth: 1

  - name: "My Calibre Library"
    url: "https://mycalibre.example.com/opds"
    auth:
      username: "user"
      password: "secret"
    poll_depth: 2
```

### Config reference

| Field | Description | Default |
|---|---|---|
| `server.addr` | Listen address | `:8080` |
| `server.title` | Root catalog title | `OPDS Aggregator` |
| `server.auth` | Basic Auth credentials for the aggregator (omit to disable) | — |
| `server.default_max_entries` | Default max entries per page for server-side pagination (0 = unlimited) | `0` |
| `polling.interval` | How often to re-crawl upstream feeds (Go duration) | `6h` |
| `feeds[].name` | Display name for the source | required |
| `feeds[].url` | OPDS catalog root URL | required |
| `feeds[].auth` | Basic Auth credentials for this upstream | — |
| `feeds[].poll_depth` | How many levels of navigation to pre-crawl (0 = root only) | `0` |
| `feeds[].max_entries` | Max entries per page for this feed (0 = use server default) | `0` |
| `feeds[].max_paginate` | Max upstream pages to follow when fetching (0 = all) | `0` |

**poll_depth tip**: Use `0` for large catalogs like Gutenberg (sub-feeds are fetched on demand). Use `1`–`2` for small personal libraries to pre-populate the cache.

**Pagination tip**: For large catalogs (e.g., Gutenberg with 70k+ entries), set `max_entries: 50` and `max_paginate: 1` to prevent hangs. The aggregator will serve paginated responses with `rel="next"` links that clients can follow.

### Environment variables

All settings can be configured via environment variables, which override YAML values. If no config file is found, the application runs entirely from environment variables.

| Variable | Description |
|----------|-------------|
| `OPDS_SERVER_ADDR` | Listen address (e.g., `:8080`) |
| `OPDS_SERVER_TITLE` | Root catalog title |
| `OPDS_SERVER_DEFAULT_MAX_ENTRIES` | Default max entries per page (0 = unlimited) |
| `OPDS_AUTH_USERNAME` | Basic Auth username |
| `OPDS_AUTH_PASSWORD` | Basic Auth password |
| `OPDS_POLLING_INTERVAL` | Refresh interval (Go duration, e.g., `6h`) |
| `OPDS_DEBUG` | Set to `true` for debug logging |

Feeds are configured with indexed variables:

| Variable | Description |
|----------|-------------|
| `OPDS_FEED_0_NAME` | First feed's display name |
| `OPDS_FEED_0_URL` | First feed's OPDS URL |
| `OPDS_FEED_0_POLL_DEPTH` | First feed's crawl depth |
| `OPDS_FEED_0_MAX_ENTRIES` | First feed's max entries per page |
| `OPDS_FEED_0_MAX_PAGINATE` | First feed's max upstream pages to follow |
| `OPDS_FEED_0_AUTH_USERNAME` | First feed's upstream auth username |
| `OPDS_FEED_0_AUTH_PASSWORD` | First feed's upstream auth password |

Increment the index for additional feeds (`OPDS_FEED_1_*`, `OPDS_FEED_2_*`, etc.). If any `OPDS_FEED_*` variables are set, they replace all YAML-defined feeds.

## Running

```sh
# With default config location
./opds-aggregator

# With explicit config
./opds-aggregator --config /path/to/config.yaml
```

The server performs an initial crawl of all feeds on startup, then polls at the configured interval.

## Docker

Container images are published to GitHub Container Registry for `linux/amd64` and `linux/arm64`.

```sh
# Pull the latest image
docker pull ghcr.io/madeddie/opds-aggregator:latest

# Run with a config file
docker run -d \
  -p 8080:8080 \
  -v /path/to/config.yaml:/config.yaml:ro \
  ghcr.io/madeddie/opds-aggregator --config /config.yaml

# Run with environment variables only
docker run -d \
  -p 8080:8080 \
  -e OPDS_AUTH_USERNAME=reader \
  -e OPDS_AUTH_PASSWORD=secret \
  -e OPDS_FEED_0_NAME="Standard Ebooks" \
  -e OPDS_FEED_0_URL="https://standardebooks.org/feeds/opds" \
  -e OPDS_FEED_0_POLL_DEPTH=1 \
  ghcr.io/madeddie/opds-aggregator
```

### Docker Compose

```yaml
services:
  opds-aggregator:
    image: ghcr.io/madeddie/opds-aggregator:latest
    ports:
      - "8080:8080"
    environment:
      - OPDS_AUTH_USERNAME=reader
      - OPDS_AUTH_PASSWORD=changeme
      - OPDS_POLLING_INTERVAL=6h
      - OPDS_FEED_0_NAME=Standard Ebooks
      - OPDS_FEED_0_URL=https://standardebooks.org/feeds/opds
      - OPDS_FEED_0_POLL_DEPTH=1
    restart: unless-stopped
```

### KOReader setup

In KOReader, go to **Search > OPDS catalog** and add a new catalog:

- **Catalog name**: whatever you like
- **Catalog URL**: `http://your-server:8080/opds`

If you configured auth, KOReader will prompt for username and password on first access.

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/opds` | Catalog root (navigation feed listing all sources) |
| `GET` | `/opds/source/{slug}/...` | Browse a specific source's feeds |
| `GET` | `/opds/download/{slug}?url=...` | Proxied download (books, covers) |
| `GET` | `/opds/search?q=...` | Search across all sources |
| `GET` | `/opds/search/{slug}?q=...&upstream=...` | Search within one source |
| `POST` | `/opds/refresh` | Trigger manual refresh of all feeds |
| `POST` | `/opds/refresh/{slug}` | Trigger manual refresh of one feed |

## License

MIT
