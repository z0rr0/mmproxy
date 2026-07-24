// Command mmproxy proxies messages into a Mattermost channel from a Telegram bot
// (forwarded messages) and Miniflux webhooks (new feed entries).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/z0rr0/mmproxy/internal/config"
	"github.com/z0rr0/mmproxy/internal/mattermost"
	"github.com/z0rr0/mmproxy/internal/server"
	"github.com/z0rr0/mmproxy/internal/telegram"
)

// name is the service name shown in the build info line.
const name = "MMProxy"

// Build metadata: Version, Revision and BuildDate are injected at build time via
// -ldflags "-X main.Version=... -X main.Revision=... -X main.BuildDate=...", so
// they must stay package-level variables — the linker cannot write anywhere else.
//
//nolint:gochecknoglobals // written by the linker at link time
var (
	// Version is a git version.
	Version = "v0.0.0"
	// Revision is a revision number.
	Revision = "git:0000000"
	// BuildDate is a build date.
	BuildDate = "1970-01-01T00:00:00"
	// GoVersion is a runtime Go language version.
	GoVersion = runtime.Version() // "go1.00.0"
)

// versionInfo returns the one-line build info, e.g.
// "MMProxy: v0.0.1 git:195a144 go1.26.5 2026-07-15T09:34:03".
func versionInfo() string {
	return fmt.Sprintf("%s: %s %s %s %s", name, Version, Revision, GoVersion, BuildDate)
}

type mattermostClient interface {
	Ping(ctx context.Context) error
	Post(ctx context.Context, channelID, message string) error
}

type telegramBot interface {
	Start(ctx context.Context)
	Shutdown(ctx context.Context) error
}

type httpServer interface {
	Addr() string
	ListenAndServe() error
	Shutdown(ctx context.Context) error
}

type appDeps struct {
	newMattermost   func(baseURL, token string, timeout time.Duration) (mattermostClient, error)
	newTelegram     func(token string, handler *telegram.Handler) (telegramBot, error)
	newHTTPServer   func(cfg *config.Config, poster server.Poster, version string) httpServer
	shutdownTimeout time.Duration
}

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML config file")
	showVersion := flag.Bool("version", false, "show version and exit")
	flag.Parse()

	// Must run before config.Load: -version has to work without a config file.
	if *showVersion {
		_, _ = fmt.Fprintln(os.Stdout, versionInfo())
		flag.PrintDefaults()
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	setupLogger(os.Stdout, cfg.Base.Debug)
	slog.Info("starting",
		"version", Version,
		"revision", Revision,
		"go", GoVersion,
		"build", BuildDate,
		"addr", cfg.Base.Addr,
		"telegram", cfg.Telegram.Enabled(),
		"miniflux", cfg.Miniflux.Enabled(),
	)

	if err = run(cfg); err != nil {
		slog.Error("failed to run", "error", err)
		os.Exit(2)
	}
	slog.Info("stopped")
}

func run(cfg *config.Config) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runContext(ctx, cfg, productionDeps(cfg))
}

func productionDeps(cfg *config.Config) appDeps {
	return appDeps{
		newMattermost: func(baseURL, token string, timeout time.Duration) (mattermostClient, error) {
			return mattermost.New(baseURL, token, timeout)
		},
		newTelegram: func(token string, handler *telegram.Handler) (telegramBot, error) {
			return telegram.NewBot(token, handler)
		},
		newHTTPServer: func(cfg *config.Config, poster server.Poster, version string) httpServer {
			return server.New(cfg, poster, version)
		},
		shutdownTimeout: cfg.Base.ShutdownTimeout.Timed(),
	}
}

func runContext(ctx context.Context, cfg *config.Config, deps appDeps) error {
	mm, err := deps.newMattermost(cfg.Mattermost.URL, cfg.Mattermost.Token, cfg.Mattermost.Timeout.Timed())
	if err != nil {
		return fmt.Errorf("create mattermost client: %w", err)
	}
	if pingErr := mm.Ping(ctx); pingErr != nil {
		return pingErr
	}

	var tg telegramBot
	stopTelegram := func() {}
	tgDone := make(chan struct{})

	if cfg.Telegram.Enabled() {
		handler := telegram.NewHandler(mm, cfg.Telegram.ChannelID, cfg.Telegram.AllowedIDs)
		tg, err = deps.newTelegram(cfg.Telegram.Token, handler)
		if err != nil {
			return fmt.Errorf("create telegram bot: %w", err)
		}
		telegramCtx, cancelTelegram := context.WithCancel(context.WithoutCancel(ctx))
		stopTelegram = cancelTelegram
		go func() {
			slog.Info("telegram bot started")
			tg.Start(telegramCtx)
			close(tgDone)
		}()
	} else {
		close(tgDone)
	}

	srv := deps.newHTTPServer(cfg, mm, versionInfo())
	listenDone := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", srv.Addr())
		listenDone <- srv.ListenAndServe()
	}()

	var serveErr error
	select {
	case listenErr := <-listenDone:
		if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			serveErr = listenErr
		}
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	}

	// Stop both ingress paths promptly, then drain their in-flight work.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), deps.shutdownTimeout)
	defer shutdownCancel()
	httpShutdownDone := make(chan error, 1)
	go func() { httpShutdownDone <- srv.Shutdown(shutdownCtx) }()

	// Stop accepting Telegram updates while the HTTP server drains in-flight
	// requests. Both sources share the same shutdown deadline.
	stopTelegram()
	select {
	case <-tgDone:
	case <-shutdownCtx.Done():
		slog.Error("telegram polling shutdown error", "error", shutdownCtx.Err())
	}
	select {
	case shutdownErr := <-httpShutdownDone:
		if shutdownErr != nil {
			slog.Error("http server shutdown error", "error", shutdownErr)
		}
	case <-shutdownCtx.Done():
		slog.Error("http server shutdown error", "error", shutdownCtx.Err())
	}

	if tg != nil {
		if err = tg.Shutdown(shutdownCtx); err != nil {
			slog.Error("telegram handlers shutdown error", "error", err)
		}
	}

	return serveErr
}

// setupLogger installs a global slog text handler. Debug mode lowers the level
// and adds source locations; timestamps use RFC3339Nano and source paths are
// shortened to the base file name.
func setupLogger(w io.Writer, debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: debug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.SourceKey:
				if src, ok := a.Value.Any().(*slog.Source); ok {
					src.File = filepath.Base(src.File)
					return slog.Any(slog.SourceKey, src)
				}
			case slog.TimeKey:
				return slog.String(slog.TimeKey, a.Value.Time().Format(time.RFC3339Nano))
			}
			return a
		},
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, opts)))
}
