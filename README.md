# OPDS Aggregator

A single-binary Go server that combines multiple OPDS 1.2 catalogs into one unified feed. Point your e-reader (KOReader, etc.) at a single URL and browse all your book sources in one place.

## Features

- **Unified catalog** — each upstream feed appears as a top-level entry, with its full structure preserved underneath
- **Download proxying** — all acquisitions (book downloads, cover images) are proxied through the aggregator, with optional disk caching
- **Basic Auth** — protect the aggregator with a username/password; per-source upstream credentials supported
- **Periodic polling** — configurable automatic refresh of upstream feeds, plus a manual refresh endpoint
- **Search** — fan-out proxy search across upstream OpenSearch endpoints, merged into a single result feed
- **On-demand fetching** — uncached sub-feeds are fetched transparently when a client navigates to them
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

polling:
  interval: "6h"

feeds:
  - name: "Project Gutenberg"
    url: "https://m.gutenberg.org/ebooks.opds/"
    poll_depth: 0

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
| `polling.interval` | How often to re-crawl upstream feeds (Go duration) | `6h` |
| `feeds[].name` | Display name for the source | required |
| `feeds[].url` | OPDS catalog root URL | required |
| `feeds[].auth` | Basic Auth credentials for this upstream | — |
| `feeds[].poll_depth` | How many levels of navigation to pre-crawl (0 = root only) | `0` |

**poll_depth tip**: Use `0` for large catalogs like Gutenberg (sub-feeds are fetched on demand). Use `1`–`2` for small personal libraries to pre-populate the cache.

## Running

```sh
# With default config location
./opds-aggregator

# With explicit config
./opds-aggregator --config /path/to/config.yaml
```

The server performs an initial crawl of all feeds on startup, then polls at the configured interval.

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
