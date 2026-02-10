# Plan: Environment Variable Configuration

## Goal
Allow the application to be configured entirely via environment variables, or use env vars to override YAML values. This supports 12-factor app deployments (Docker, systemd, etc.) where config files may not be practical.

## Design Decisions

### Precedence: env vars override YAML
- If a YAML file exists, it is loaded first as the base config
- Environment variables override any YAML-provided values
- If no YAML file exists and no `--config` flag is given, build config purely from env vars
- The app only errors out if, after merging both sources, validation still fails (e.g. no feeds)

### Env var naming convention
All env vars use the prefix `OPDS_` with underscores separating hierarchy levels:

**Server settings:**
| Env Var | Maps to | Example |
|---------|---------|---------|
| `OPDS_SERVER_ADDR` | `server.addr` | `:9090` |
| `OPDS_SERVER_TITLE` | `server.title` | `My Library` |
| `OPDS_AUTH_USERNAME` | `server.auth.username` | `reader` |
| `OPDS_AUTH_PASSWORD` | `server.auth.password` | `secret` |

**Polling:**
| Env Var | Maps to | Example |
|---------|---------|---------|
| `OPDS_POLLING_INTERVAL` | `polling.interval` | `1h` |

**Feeds (indexed, 0-based):**
| Env Var | Maps to | Example |
|---------|---------|---------|
| `OPDS_FEED_0_NAME` | `feeds[0].name` | `Project Gutenberg` |
| `OPDS_FEED_0_URL` | `feeds[0].url` | `https://...` |
| `OPDS_FEED_0_POLL_DEPTH` | `feeds[0].poll_depth` | `0` |
| `OPDS_FEED_0_AUTH_USERNAME` | `feeds[0].auth.username` | `user` |
| `OPDS_FEED_0_AUTH_PASSWORD` | `feeds[0].auth.password` | `pass` |

Feed indices must be contiguous starting from 0. The parser stops scanning at the first missing `OPDS_FEED_N_NAME`.

### Debug flag
| Env Var | Maps to | Example |
|---------|---------|---------|
| `OPDS_DEBUG` | `--debug` flag | `true` |

CLI `--debug` flag takes precedence if explicitly set; otherwise `OPDS_DEBUG=true` enables debug logging.

## Implementation Steps

### 1. Add `ApplyEnv` method to `config/config.go`

Add a new exported function `ApplyEnv(cfg *Config)` that reads environment variables and overlays them onto the config struct:

- Check each `OPDS_*` env var; if set and non-empty, overwrite the corresponding field
- For feeds: scan `OPDS_FEED_0_NAME`, `OPDS_FEED_1_NAME`, ... until a gap is found
- If env-defined feeds exist, they **replace** the YAML feeds entirely (mixing indexed feeds from two sources would be confusing)
- For server auth: if either `OPDS_AUTH_USERNAME` or `OPDS_AUTH_PASSWORD` is set, create/overwrite the `AuthConfig`
- For per-feed auth: same logic per feed index

### 2. Update `main.go` startup flow

Change the config loading logic:

```
1. Try to load YAML (from --config flag or auto-detect)
2. If YAML found → load it, then apply env var overrides
3. If no YAML found → start with empty Config, apply env vars
4. Apply defaults
5. Validate
6. If invalid → error out with helpful message mentioning both config file and env vars
```

Also check `OPDS_DEBUG` env var for debug logging when `--debug` flag is not set.

### 3. Update `config.example.yaml` with comments

Add a comment block at the top documenting that all settings can be overridden via `OPDS_*` environment variables, with a reference to the naming convention.

### 4. Files changed

- `config/config.go` — add `ApplyEnv()` function, refactor `Load()` to support optional YAML
- `main.go` — update startup to use env var fallback and `OPDS_DEBUG`
- `config.example.yaml` — add env var documentation comments
