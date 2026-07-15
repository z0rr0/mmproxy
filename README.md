# MMProxy

![Go](https://github.com/z0rr0/mmproxy/workflows/Go/badge.svg)
![Version](https://img.shields.io/github/tag/z0rr0/mmproxy.svg)
![License](https://img.shields.io/github/license/z0rr0/mmproxy.svg)

MMProxy proxies messages into a Mattermost channel from two independent sources:

- **Telegram bot** — forward any message to the bot and it republishes the text
  into Mattermost.
- **Miniflux webhook** — Miniflux pushes new feed entries; MMProxy posts them.

Each source is optional; enable one or both. Processing is synchronous and
retry-free: delivery errors are returned to the caller.

## Endpoints

| Method & path   | Purpose |
|-----------------|---------|
| `GET /health`   | Liveness probe, returns `200 OK`. |
| `GET /version`  | Service version (injected at build time). |
| `POST /miniflux`| Miniflux webhook receiver (only when the source is enabled). |

## Quick start

```bash
# 1. Prepare the config
cp docs/config.toml config.toml
$EDITOR config.toml            # fill in Mattermost / Telegram / Miniflux values

# 2. Build and run
make build
./mmproxy -config config.toml
```

Check it:

```bash
curl -i localhost:8080/health     # 200 OK
curl -i localhost:8080/version
```

### Docker

```bash
cp docs/config.toml config.toml   # edit it, then:
LDFLAGS="-X main.version=$(git describe --tags --always --dirty)" docker compose up -d --build
```

The compose file mounts `./config.toml` read-only at `/data/config.toml`, checks
`/health` on the default internal port 8080, and restarts the container
`unless-stopped`. Docker records an unhealthy status but does not restart a
running unhealthy container solely because of the health check.

## Configuration

TOML, passed via `-config` (default `config.toml`). See
[`docs/config.toml`](docs/config.toml) for the annotated example. Key points:

- `[mattermost]` `url`, `token`, `channel_id` are always required. Prefer a
  dedicated bot account that is a member of the target channel.
- `[telegram]` is enabled when `token` is set; `allowed_users` (numeric Telegram
  IDs) is then required. Empty per-source `channel_id` falls back to the shared
  Mattermost channel.
- `[miniflux]` is enabled when `webhook_secret` is set. Requests are
  authenticated by the `X-Miniflux-Signature` HMAC-SHA256 header. `feed_ids`
  optionally restricts which feeds are accepted.
- Timeouts are Go duration strings: `[base]` `read_timeout`,
  `read_header_timeout`, `write_timeout`, `idle_timeout`, `shutdown_timeout` and
  `[mattermost]` `timeout`. Each is optional and falls back to its default;
  `base.write_timeout` must exceed `mattermost.timeout`, otherwise startup fails.

See [`docs/notes.md`](docs/notes.md) for message formats, webhook response codes
and the v1 trade-offs.

## Development

```bash
make fmt      # gofmt
make lint     # gofmt check + go vet + golangci-lint + non-blocking govulncheck
make vuln     # informational govulncheck (never blocks the build)
make test     # lint + go test -race -cover ./...
make build    # binary with version injected via ldflags
make docker   # build the container image
make clean
```

Requires Go 1.26 and (for linting) `golangci-lint`. GitHub Actions runs build,
vet, race-enabled tests and lint on pushes and pull requests to `main`;
`govulncheck` is reported separately without blocking CI.

## License

This source code is governed by a [MIT](https://opensource.org/license/MIT)
license that can be found in the [LICENSE](https://github.com/z0rr0/mmproxy/blob/main/LICENSE) file.
