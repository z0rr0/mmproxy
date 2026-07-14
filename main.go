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

	mm := mattermost.New(cfg.Mattermost.URL, cfg.Mattermost.Token)
	if err := mm.Ping(ctx); err != nil {
		return err
	}

	// Telegram long polling stops when ctx is cancelled. When the source is
	// disabled, tgDone is closed immediately so shutdown never blocks on it.
	tgDone := make(chan struct{})
	if cfg.Telegram.Enabled() {
		handler := telegram.NewHandler(mm, cfg.Telegram.ChannelID, cfg.Telegram.AllowedIDs)
		b, err := telegram.NewBot(cfg.Telegram.Token, handler)
		if err != nil {
			return fmt.Errorf("create telegram bot: %w", err)
		}
		go func() {
			slog.Info("telegram bot started")
			b.Start(ctx)
			close(tgDone)
		}()
	} else {
		close(tgDone)
	}

	srv := server.New(cfg, mm, version)
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

	// Reverse-order shutdown: stop the HTTP server first, then the bot.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http server shutdown error", "error", err)
	}
	cancel()
	<-tgDone

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
