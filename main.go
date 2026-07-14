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
	"syscall"
	"time"

	"github.com/z0rr0/mmproxy/internal/config"
	"github.com/z0rr0/mmproxy/internal/mattermost"
	"github.com/z0rr0/mmproxy/internal/server"
	"github.com/z0rr0/mmproxy/internal/telegram"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "v0.0.1"

const shutdownTimeout = 10 * time.Second

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
	newMattermost   func(baseURL, token string) (mattermostClient, error)
	newTelegram     func(token string, handler *telegram.Handler) (telegramBot, error)
	newHTTPServer   func(cfg *config.Config, poster server.Poster, version string) httpServer
	shutdownTimeout time.Duration
}

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	setupLogger(os.Stdout, cfg.Base.Debug)
	slog.Info("starting",
		"version", version,
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
	return runContext(ctx, cfg, productionDeps())
}

func productionDeps() appDeps {
	return appDeps{
		newMattermost: func(baseURL, token string) (mattermostClient, error) {
			return mattermost.New(baseURL, token)
		},
		newTelegram: func(token string, handler *telegram.Handler) (telegramBot, error) {
			return telegram.NewBot(token, handler)
		},
		newHTTPServer: func(cfg *config.Config, poster server.Poster, version string) httpServer {
			return server.New(cfg, poster, version)
		},
		shutdownTimeout: shutdownTimeout,
	}
}

func runContext(ctx context.Context, cfg *config.Config, deps appDeps) error {
	mm, err := deps.newMattermost(cfg.Mattermost.URL, cfg.Mattermost.Token)
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

	srv := deps.newHTTPServer(cfg, mm, version)
	listenDone := make(chan error, 1)
	go func() {
		slog.Info("http server listening", "addr", srv.Addr())
		listenDone <- srv.ListenAndServe()
	}()

	var serveErr error
	select {
	case err := <-listenDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
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
		if err := tg.Shutdown(shutdownCtx); err != nil {
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
