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

The compose file mounts `./config.toml` read-only at `/data/config.toml` and
restarts the container `unless-stopped`.

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

See [`docs/notes.md`](docs/notes.md) for message formats, webhook response codes
and the v1 trade-offs.

## Development

```bash
make fmt      # gofmt
make lint     # gofmt check + go vet + golangci-lint + govulncheck
make test     # lint + go test -race -cover ./...
make build    # binary with version injected via ldflags
make docker   # build the container image
make clean
```

Requires Go 1.26 and (for linting) `golangci-lint`.

## License

This source code is governed by a [MIT](https://opensource.org/license/MIT)
license that can be found in the [LICENSE](https://github.com/z0rr0/mmproxy/blob/main/LICENSE) file.
