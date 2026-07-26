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
make docker   # z0rr0/mmproxy:latest + :$(TAG), host arch, --load
make docker-push                   # multi-arch, straight to Docker Hub
go test -race ./internal/server/   # single package
```

Lint config is `golangci.yml` (v2, `default: all` minus a small disable list).
`fieldalignment` is intentionally disabled so config structs mirror their TOML
sections.

## Package map

| Package               | Responsibility                                                                      |
|-----------------------|-------------------------------------------------------------------------------------|
| `main`                | flags (`-config`, `-version`), build metadata + `versionInfo()`, `setupLogger`, `run()` → `runContext(ctx, cfg, appDeps)` orchestration, graceful shutdown |
| `internal/config`     | TOML load + `errors.Join` validation, derived allowlist maps, channel normalization |
| `internal/mattermost` | stdlib `net/http` client for the two endpoints we use: `New`, `Ping`, `Post`, `Truncate` |
| `internal/markdown`   | `EscapeText`: flattens and escapes single-line labels embedded in Markdown          |
| `internal/telegram`   | bot `Handler`, forwarded-message formatting, `NewBot`                               |
| `internal/miniflux`   | webhook types, `ValidSignature` (HMAC), `FormatPost`                                |
| `internal/server`     | HTTP mux, health/version/miniflux handlers, logging + recover middleware            |

`appDeps` (`main.go`) holds the constructor seams — `newMattermost`, `newTelegram`,
`newHTTPServer` — so `runContext` can be tested against fakes; `productionDeps` wires
the real packages.

## Conventions

- **Consumer-side interfaces**: `Poster` and `BotAPI` are declared where they are
  used (`server`, `telegram`), not exported by `mattermost`. Concrete
  `*mattermost.Client` satisfies them.
- **Thin `main` → `run(cfg) error`**: `os.Exit` only at the top level.
- **One global `slog`**: `setupLogger` calls `slog.SetDefault`; everything else
  uses `slog.Info`/`Warn`/`Error` directly.
- **Middleware order** in `server.New`: `RecoverMiddleware(LoggingMiddleware(mux))`.
  `LoggingMiddleware` recovers handler panics itself, because it has to record the
  final status of the request it is logging; `RecoverMiddleware` stays outermost as a
  last line of defense for panics raised by logging.
- **Channel normalization** happens in `config`: empty per-source `channel_id`
  is resolved to the shared one during validation, so handlers never see the
  fallback.
- **Request ID via context**: `LoggingMiddleware` stores the ID under the private
  `requestIDKey` (type `contextKey`, `middleware.go`); handlers read it with
  `requestIDFrom(r.Context())` and log it as `request_id`, so handler lines join up
  with the `request completed` line. `RecoverMiddleware` cannot see it — it wraps
  the logging middleware, so the ID does not exist yet at that layer. A client
  `X-Request-ID` is reused only when `sanitizeRequestID` accepts it (≤64 chars of
  `[A-Za-z0-9_-]`); the value is echoed back in a header and written to logs, so
  anything else is replaced by a generated ID.

## Gotchas

- **HMAC over the raw body, before JSON**: `handleMiniflux` reads the full body,
  verifies `X-Miniflux-Signature`, and only then unmarshals. Do not reorder — the
  signature covers the exact bytes and unauthenticated input must not be parsed.
- **Truncation is by runes**, not bytes (`mattermost.Truncate`) — safe for
  multibyte text. Both sources go through `mattermost.Post`, so this is
  centralized. The limit comes from `[mattermost] max_message_runes`; 16383 is
  duplicated as a default in **two** places — `config.defaultMaxMessageRunes`
  (applied in `applyDefaults`) and `mattermost.defaultMaxMessageRunes` (the
  fallback in `New` for non-positive input, so the package stays usable without
  `config`, same as `defaultTimeout`). Keep them in sync.
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
- **`*mattermost.Client` is concurrency-safe**: it holds one `*http.Client`, and the
  token lives in a field that `newRequest` turns into an `Authorization` header per
  call — nothing is mutated after construction, so the Telegram handler and the
  webhook can post simultaneously. The per-call deadline comes from
  `context.WithTimeout` inside `Ping`/`Post`, not from `http.Client.Timeout`.
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
- **No Mattermost SDK**: the client is ~190 lines of stdlib `net/http` against
  `GET /api/v4/users/me` and `POST /api/v4/posts`, so `go.mod` stays at two direct
  dependencies (`go-telegram/bot`, `go-toml/v2`). Keep it that way — pulling in
  `server/public` would add ~40 transitive modules for two endpoints.
  `govulncheck` is advisory in both places it runs: the `-` prefix on the `vuln`
  target in the Makefile and `continue-on-error: true` in CI.
- Webhook response codes are specified in `docs/notes.md`; `server_test.go`
  pins each one.
- **Two docker targets, on purpose**: `make docker` builds only the host arch and
  `--load`s it, because the default Docker Desktop builder (`docker` driver,
  overlay2 image store) cannot export a multi-arch manifest list locally.
  `make docker-push` builds `linux/amd64,linux/arm64` on its own
  `docker-container` builder (`$(BUILDER)`, created idempotently on first use)
  and pushes straight to the registry — `docker-push` deliberately does **not**
  depend on `docker`, that would build everything twice. Both tag `$(IMAGE)`
  with `latest` and `$(TAG)`; `docker-push` refuses the `v0.0.0` fallback so an
  untagged tree cannot be published. `docker-compose.yml` no longer builds
  anything — it just runs `z0rr0/mmproxy:latest`.
