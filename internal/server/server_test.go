package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/z0rr0/mmproxy/internal/config"
)

type mockPoster struct {
	calls   int
	channel string
	message string
	err     error
}

func (m *mockPoster) Post(_ context.Context, channelID, message string) error {
	m.calls++
	m.channel = channelID
	m.message = message
	return m.err
}

const webhookSecret = "topsecret"

func sign(body string) string {
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	// hash.Hash.Write is documented never to return an error.
	_, _ = mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// newTestServer builds a Server wired to a mock poster and serves its full
// middleware+mux handler over httptest.
func newTestServer(t *testing.T, poster Poster, feedIDs []int64) *httptest.Server {
	t.Helper()
	cfg := &config.Config{
		Base:       config.Base{Addr: ":0"},
		Mattermost: config.Mattermost{URL: "http://mm", Token: "t", ChannelID: "shared"},
		Miniflux: config.Miniflux{
			WebhookSecret: webhookSecret,
			ChannelID:     "mf-channel",
			AllowedFeeds:  sliceToSet(feedIDs),
		},
	}
	s := New(cfg, poster, "v-test")
	ts := httptest.NewServer(s.srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func sliceToSet(ids []int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func post(t *testing.T, url, sig, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if sig != "" {
		req.Header.Set("X-Miniflux-Signature", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	closeBody(t, resp)
	return resp
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	closeBody(t, resp)
	return resp
}

// closeBody defers the body close to test teardown and reports a failed close
// instead of dropping it: a broken close points at the server, not at whatever
// the test is asserting, so it is logged rather than turned into a failure.
func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Logf("close response body: %v", err)
		}
	})
}

// readBody drains the response so a read error surfaces as itself instead of as
// a body-mismatch assertion further down.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(body)
}

func TestHealthAndVersion(t *testing.T) {
	ts := newTestServer(t, &mockPoster{}, []int64{1})

	resp := get(t, ts.URL+"/health")
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || body != "OK" {
		t.Errorf("/health = %d %q, want 200 OK", resp.StatusCode, body)
	}

	resp = get(t, ts.URL+"/version")
	if body := readBody(t, resp); resp.StatusCode != http.StatusOK || body != "v-test" {
		t.Errorf("/version = %d %q, want 200 v-test", resp.StatusCode, body)
	}
	if resp.Header.Get(requestIDHeader) == "" {
		t.Error("missing X-Request-ID header")
	}
}

func TestServerTimeouts(t *testing.T) {
	// Distinct values so a mixed-up field assignment cannot pass.
	cfg := &config.Config{
		Base: config.Base{
			Addr:              ":0",
			ReadTimeout:       config.Duration(11 * time.Second),
			ReadHeaderTimeout: config.Duration(3 * time.Second),
			WriteTimeout:      config.Duration(31 * time.Second),
			IdleTimeout:       config.Duration(61 * time.Second),
		},
		Mattermost: config.Mattermost{URL: "http://mm", Token: "t", ChannelID: "shared"},
	}
	s := New(cfg, &mockPoster{}, "v-test")

	tests := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"ReadTimeout", s.srv.ReadTimeout, 11 * time.Second},
		{"ReadHeaderTimeout", s.srv.ReadHeaderTimeout, 3 * time.Second},
		{"WriteTimeout", s.srv.WriteTimeout, 31 * time.Second},
		{"IdleTimeout", s.srv.IdleTimeout, 61 * time.Second},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
	if s.srv.MaxHeaderBytes != maxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", s.srv.MaxHeaderBytes, maxHeaderBytes)
	}
}

func TestMinifluxHappyPath(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, []int64{1})

	body := `{"event_type":"new_entries","feed":{"id":1,"title":"Feed"},"entries":[{"title":"A","url":"https://e.com/a"},{"title":"B","url":"https://e.com/b"}]}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if poster.calls != 1 {
		t.Fatalf("poster called %d times, want 1", poster.calls)
	}
	if poster.channel != "mf-channel" {
		t.Errorf("channel = %q, want mf-channel", poster.channel)
	}
	want := "#### Feed\n- [A](https://e.com/a)\n- [B](https://e.com/b)"
	if poster.message != want {
		t.Errorf("message = %q, want %q", poster.message, want)
	}
}

func TestMinifluxBadSignature(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, []int64{1})

	body := `{"event_type":"new_entries","feed":{"id":1},"entries":[{"title":"A","url":"u"}]}`
	resp := post(t, ts.URL+"/miniflux", "deadbeef", body)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
}

func TestMinifluxBadJSON(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, []int64{1})

	body := `{"event_type":"new_entries", broken`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
}

func TestMinifluxSaveEntryIgnored(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, []int64{1})

	body := `{"event_type":"save_entry","entry":{"id":5}}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
}

func TestMinifluxFeedFiltered(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, []int64{1, 2})

	body := `{"event_type":"new_entries","feed":{"id":99},"entries":[{"title":"X","url":"u"}]}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
}

// safeBuffer guards the log buffer: the handler writes from the httptest server
// goroutine while the test reads from its own.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestMinifluxFeedFilteredLogsRequestID pins the handler log to the same request
// ID the middleware reports, so a filtered feed can be traced back to its request.
func TestMinifluxFeedFilteredLogsRequestID(t *testing.T) {
	var logs safeBuffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	ts := newTestServer(t, &mockPoster{}, []int64{1, 2})

	body := `{"event_type":"new_entries","feed":{"id":99,"title":"Blocked"},"entries":[{"title":"X","url":"u"}]}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)

	requestID := resp.Header.Get(requestIDHeader)
	if requestID == "" {
		t.Fatal("missing X-Request-ID header")
	}

	var found bool
	want := "request_id=" + requestID
	for line := range strings.SplitSeq(logs.String(), "\n") {
		if !strings.Contains(line, "miniflux feed filtered") {
			continue
		}
		found = true
		if !strings.Contains(line, want) {
			t.Errorf("filtered log %q missing %q", line, want)
		}
	}
	if !found {
		t.Errorf("no %q log line: %s", "miniflux feed filtered", logs.String())
	}
}

func TestMinifluxEmptyAllowlistAcceptsAll(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, nil) // empty allowlist

	body := `{"event_type":"new_entries","feed":{"id":12345},"entries":[{"title":"X","url":"u"}]}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if poster.calls != 1 {
		t.Errorf("poster called %d times, want 1", poster.calls)
	}
}

func TestMinifluxEmptyEntries(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, []int64{1})

	body := `{"event_type":"new_entries","feed":{"id":1},"entries":[]}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
}

func TestMinifluxPosterError(t *testing.T) {
	poster := &mockPoster{err: errors.New("upstream down")}
	ts := newTestServer(t, poster, []int64{1})

	body := `{"event_type":"new_entries","feed":{"id":1},"entries":[{"title":"A","url":"u"}]}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestMinifluxBodyTooLarge(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, []int64{1})

	big := strings.Repeat("a", (4<<20)+1)
	resp := post(t, ts.URL+"/miniflux", sign(big), big)

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
}

func TestMinifluxMethodNotAllowed(t *testing.T) {
	ts := newTestServer(t, &mockPoster{}, []int64{1})

	resp := get(t, ts.URL+"/miniflux")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestMinifluxDisabledReturns404(t *testing.T) {
	cfg := &config.Config{
		Base:       config.Base{Addr: ":0"},
		Mattermost: config.Mattermost{URL: "http://mm", Token: "t", ChannelID: "shared"},
		Miniflux:   config.Miniflux{}, // disabled: empty secret
	}
	s := New(cfg, &mockPoster{}, "v-test")
	ts := httptest.NewServer(s.srv.Handler)
	// Cleanup, not defer: post registers its body close as a later cleanup, and
	// LIFO order then closes the body before Close waits on the connection.
	t.Cleanup(ts.Close)

	body := `{"event_type":"new_entries"}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when miniflux disabled", resp.StatusCode)
	}
}
