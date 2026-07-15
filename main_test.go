package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/z0rr0/mmproxy/internal/config"
	"github.com/z0rr0/mmproxy/internal/server"
	"github.com/z0rr0/mmproxy/internal/telegram"
)

type fakeMattermost struct {
	pingErr   error
	pingCalls int
}

func (m *fakeMattermost) Ping(context.Context) error {
	m.pingCalls++
	return m.pingErr
}

func (*fakeMattermost) Post(context.Context, string, string) error { return nil }

type fakeTelegram struct {
	started        chan struct{}
	pollingStopped chan struct{}
	shutdownCalls  int
}

func (b *fakeTelegram) Start(ctx context.Context) {
	close(b.started)
	<-ctx.Done()
	close(b.pollingStopped)
}

func (b *fakeTelegram) Shutdown(context.Context) error {
	b.shutdownCalls++
	return nil
}

type fakeHTTPServer struct {
	started           chan struct{}
	done              chan struct{}
	listenErr         error
	shutdownStarted   chan struct{}
	shutdownRelease   chan struct{}
	shutdownOnce      sync.Once
	shutdownStartOnce sync.Once
}

func (*fakeHTTPServer) Addr() string { return ":0" }

func (s *fakeHTTPServer) ListenAndServe() error {
	close(s.started)
	if s.listenErr != nil {
		return s.listenErr
	}
	<-s.done
	return nil
}

func (s *fakeHTTPServer) Shutdown(ctx context.Context) error {
	s.shutdownStartOnce.Do(func() { close(s.shutdownStarted) })
	s.shutdownOnce.Do(func() { close(s.done) })
	if s.shutdownRelease == nil {
		return nil
	}
	select {
	case <-s.shutdownRelease:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestRunContextStopsPollingWhileHTTPDrains(t *testing.T) {
	cfg := testConfig(true)
	mm := &fakeMattermost{}
	tg := &fakeTelegram{started: make(chan struct{}), pollingStopped: make(chan struct{})}
	httpServer := &fakeHTTPServer{
		started:         make(chan struct{}),
		done:            make(chan struct{}),
		shutdownStarted: make(chan struct{}),
		shutdownRelease: make(chan struct{}),
	}
	deps := testDeps(mm, tg, httpServer)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- runContext(ctx, cfg, deps) }()
	<-tg.started
	<-httpServer.started
	cancel()
	select {
	case <-httpServer.shutdownStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP shutdown did not start")
	}
	select {
	case <-tg.pollingStopped:
	case <-time.After(time.Second):
		t.Fatal("Telegram polling continued while HTTP shutdown was blocked")
	}
	if tg.shutdownCalls != 0 {
		t.Fatal("handler shutdown ran before HTTP drain completed")
	}
	close(httpServer.shutdownRelease)

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("runContext failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runContext did not finish")
	}
	if tg.shutdownCalls != 1 {
		t.Fatalf("telegram Shutdown calls = %d, want 1", tg.shutdownCalls)
	}
}

func TestRunContextListenErrorStopsTelegram(t *testing.T) {
	cfg := testConfig(true)
	mm := &fakeMattermost{}
	tg := &fakeTelegram{started: make(chan struct{}), pollingStopped: make(chan struct{})}
	httpServer := &fakeHTTPServer{
		started:         make(chan struct{}),
		done:            make(chan struct{}),
		listenErr:       errors.New("bind failed"),
		shutdownStarted: make(chan struct{}),
	}
	deps := testDeps(mm, tg, httpServer)

	err := runContext(context.Background(), cfg, deps)
	if err == nil || err.Error() != "bind failed" {
		t.Fatalf("runContext error = %v, want bind failed", err)
	}
	if tg.shutdownCalls != 1 {
		t.Fatalf("telegram Shutdown calls = %d, want 1", tg.shutdownCalls)
	}
}

func TestRunContextPingErrorStopsStartup(t *testing.T) {
	cfg := testConfig(true)
	pingErr := errors.New("bad credentials")
	mm := &fakeMattermost{pingErr: pingErr}
	tgCreated := false
	httpCreated := false
	deps := appDeps{
		newMattermost: func(string, string, time.Duration) (mattermostClient, error) { return mm, nil },
		newTelegram: func(string, *telegram.Handler) (telegramBot, error) {
			tgCreated = true
			return nil, errors.New("unexpected")
		},
		newHTTPServer: func(*config.Config, server.Poster, string) httpServer {
			httpCreated = true
			return nil
		},
		shutdownTimeout: time.Second,
	}

	err := runContext(context.Background(), cfg, deps)
	if !errors.Is(err, pingErr) {
		t.Fatalf("runContext error = %v, want ping error", err)
	}
	if tgCreated || httpCreated {
		t.Fatalf("startup continued after ping failure: telegram=%v http=%v", tgCreated, httpCreated)
	}
}

func TestRunContextTelegramCreationErrorStopsStartup(t *testing.T) {
	cfg := testConfig(true)
	mm := &fakeMattermost{}
	createErr := errors.New("telegram unavailable")
	httpCreated := false
	deps := appDeps{
		newMattermost: func(string, string, time.Duration) (mattermostClient, error) { return mm, nil },
		newTelegram: func(string, *telegram.Handler) (telegramBot, error) {
			return nil, createErr
		},
		newHTTPServer: func(*config.Config, server.Poster, string) httpServer {
			httpCreated = true
			return nil
		},
		shutdownTimeout: time.Second,
	}

	err := runContext(context.Background(), cfg, deps)
	if !errors.Is(err, createErr) {
		t.Fatalf("runContext error = %v, want Telegram creation error", err)
	}
	if httpCreated {
		t.Fatal("HTTP server was created after Telegram startup failed")
	}
}

func TestRunContextTelegramDisabledDoesNotBlock(t *testing.T) {
	cfg := testConfig(false)
	mm := &fakeMattermost{}
	httpServer := &fakeHTTPServer{
		started:         make(chan struct{}),
		done:            make(chan struct{}),
		shutdownStarted: make(chan struct{}),
	}
	deps := testDeps(mm, nil, httpServer)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runContext(ctx, cfg, deps); err != nil {
		t.Fatalf("runContext failed: %v", err)
	}
}

func testConfig(telegramEnabled bool) *config.Config {
	cfg := &config.Config{
		Base:       config.Base{Addr: ":0"},
		Mattermost: config.Mattermost{URL: "https://mm.example.com", Token: "token", ChannelID: "channel"},
	}
	if telegramEnabled {
		cfg.Telegram = config.Telegram{
			Token:      "telegram-token",
			ChannelID:  "channel",
			AllowedIDs: map[int64]struct{}{1: {}},
		}
	}
	return cfg
}

func testDeps(mm mattermostClient, tg telegramBot, srv httpServer) appDeps {
	return appDeps{
		newMattermost: func(string, string, time.Duration) (mattermostClient, error) { return mm, nil },
		newTelegram: func(string, *telegram.Handler) (telegramBot, error) {
			return tg, nil
		},
		newHTTPServer:   func(*config.Config, server.Poster, string) httpServer { return srv },
		shutdownTimeout: time.Second,
	}
}
