# CLAUDE.md

Guidance for LLM agents working in this repository.

## What this is

MMProxy: a small Go 1.26 service that proxies messages into a Mattermost channel
from a Telegram bot (forwarded messages) and Miniflux webhooks. Synchronous,
retry-free, standard-library HTTP. See `docs/notes.md` for business logic.

## Commands

```bash
make test     # gofmt check + go vet + golangci-lint + govulncheck, then go test -race -cover ./...
make lint     # static analysis only
make build    # go build with -ldflags: -X main.Version/Revision/BuildDate (native host arch)
make dist     # cross-compile PLATFORMS (darwin/arm64, linux/amd64) into dist/
go test -race ./internal/server/   # single package
```

Lint config is `golangci.yml` (v2, `default: all` minus a small disable list).
`fieldalignment` is intentionally disabled so config structs mirror their TOML
sections.

## Package map

| Package               | Responsibility                                                                      |
|-----------------------|-------------------------------------------------------------------------------------|
| `main`                | flags (`-config`, `-version`), build metadata + `versionInfo()`, `setupLogger`, `run()` orchestration, graceful shutdown |
| `internal/config`     | TOML load + `errors.Join` validation, derived allowlist maps, channel normalization |
| `internal/mattermost` | `model.Client4` wrapper: `New`, `Ping`, `Post`, `Truncate`                          |
| `internal/telegram`   | bot `Handler`, forwarded-message formatting, `NewBot`                               |
| `internal/miniflux`   | webhook types, `ValidSignature` (HMAC), `FormatPost`                                |
| `internal/server`     | HTTP mux, health/version/miniflux handlers, logging + recover middleware            |

## Conventions

- **Consumer-side interfaces**: `Poster` and `BotAPI` are declared where they are
  used (`server`, `telegram`), not exported by `mattermost`. Concrete
  `*mattermost.Client` satisfies them.
- **Thin `main` → `run(cfg) error`**: `os.Exit` only at the top level.
- **One global `slog`**: `setupLogger` calls `slog.SetDefault`; everything else
  uses `slog.Info`/`Warn`/`Error` directly.
- **Middleware order** in `server.New`: `RecoverMiddleware(LoggingMiddleware(mux))`
  — recover is outermost so it also catches panics in logging.
- **Channel normalization** happens in `config`: empty per-source `channel_id`
  is resolved to the shared one during validation, so handlers never see the
  fallback.

## Gotchas

- **HMAC over the raw body, before JSON**: `handleMiniflux` reads the full body,
  verifies `X-Miniflux-Signature`, and only then unmarshals. Do not reorder — the
  signature covers the exact bytes and unauthenticated input must not be parsed.
- **Truncation is by runes**, not bytes (`mattermost.Truncate`, limit 16383) —
  safe for multibyte text. Both sources go through `mattermost.Post`, so this is
  centralized.
- **Build metadata via ldflags**: `Version`, `Revision` and `BuildDate` in `main`
  are overwritten by `-X main.Version=... -X main.Revision=... -X main.BuildDate=...`
  (see `Makefile`). Keep the variable names if you change the build. `GoVersion` is
  **not** an ldflag — it comes from `runtime.Version()`. `versionInfo()` joins all
  four into one line; it feeds `-version`, the startup log and `GET /version`, so
  `server.New` receives the full line, not a bare version.
- **The var block needs `//nolint:gochecknoglobals`**: the linter only whitelists
  the exact lowercase name `version`, so the exported build-metadata variables trip
  it. The directive must stay on the line immediately before `var (` and in column 1
  — golangci-lint only expands it over the whole declaration under those conditions.
  `gofmt` inserts a bare `//` line above it; that is expected and harmless.
- **`-version` is handled before `config.Load`**, otherwise it would fail on a
  missing config file instead of printing the version. It prints via
  `fmt.Fprintln(os.Stdout, ...)` — `forbidigo` bans `fmt.Println`, and the logger
  is not set up yet at that point.
- **Makefile tag fallback**: `git tag | sort -V | tail -1 | grep . || echo "v0.0.0"`.
  The `grep .` matters — CI checks out without tags, and an empty `git tag` would
  otherwise inject `-X main.Version=` and blank out the default.
- **`bot.Start(ctx)` blocks** until ctx is cancelled; it runs in a goroutine and
  shutdown cancels ctx then waits on `tgDone`. When Telegram is disabled,
  `tgDone` is closed immediately so shutdown never blocks.
- **`model.Client4` is concurrency-safe** (wraps an `http.Client`); the Telegram
  handler and the webhook can post simultaneously. `SetToken` runs once at
  construction, before goroutines start.
- **Fail-fast startup**: `run` calls `mm.Ping` (GetMe) and returns on failure, so
  a bad URL/token or down Mattermost aborts the process.
- **Timeouts come from config**, as `config.Duration` (a `time.Duration` with
  `UnmarshalText`): the four HTTP server timeouts and `shutdown_timeout` in
  `[base]`, the API request timeout in `[mattermost]`. Defaults live in
  `config.applyDefaults`; a zero value means "unset", so an explicit `"0s"` also
  becomes the default. `crossValidate` rejects
  `write_timeout <= mattermost.timeout` — the invariant used to be a comment in
  `server.go` and matters because the webhook posts synchronously inside the
  handler. `mattermost.New` keeps its own `defaultTimeout` fallback for
  non-positive input, so the package stays usable without `config`.
- **Mattermost SDK is heavy**: `server/public` pulls ~40 transitive modules.
  `govulncheck` reports vulnerabilities in those imports that our code does not
  call — expected.
- Webhook response codes are specified in `docs/notes.md`; `server_test.go`
  pins each one.
