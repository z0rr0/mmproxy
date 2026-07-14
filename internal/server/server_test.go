package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	mac.Write([]byte(body))
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
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
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
	return resp
}

func TestHealthAndVersion(t *testing.T) {
	ts := newTestServer(t, &mockPoster{}, []int64{1})

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "OK" {
		t.Errorf("/health = %d %q, want 200 OK", resp.StatusCode, body)
	}

	resp, err = http.Get(ts.URL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "v-test" {
		t.Errorf("/version = %d %q, want 200 v-test", resp.StatusCode, body)
	}
	if resp.Header.Get(requestIDHeader) == "" {
		t.Error("missing X-Request-ID header")
	}
}

func TestMinifluxHappyPath(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, []int64{1})

	body := `{"event_type":"new_entries","feed":{"id":1,"title":"Feed"},"entries":[{"title":"A","url":"https://e.com/a"},{"title":"B","url":"https://e.com/b"}]}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
}

func TestMinifluxEmptyAllowlistAcceptsAll(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, nil) // empty allowlist

	body := `{"event_type":"new_entries","feed":{"id":12345},"entries":[{"title":"X","url":"u"}]}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestMinifluxBodyTooLarge(t *testing.T) {
	poster := &mockPoster{}
	ts := newTestServer(t, poster, []int64{1})

	big := strings.Repeat("a", (4<<20)+1)
	resp := post(t, ts.URL+"/miniflux", sign(big), big)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
	if poster.calls != 0 {
		t.Errorf("poster called %d times, want 0", poster.calls)
	}
}

func TestMinifluxMethodNotAllowed(t *testing.T) {
	ts := newTestServer(t, &mockPoster{}, []int64{1})

	resp, err := http.Get(ts.URL + "/miniflux")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
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
	defer ts.Close()

	body := `{"event_type":"new_entries"}`
	resp := post(t, ts.URL+"/miniflux", sign(body), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when miniflux disabled", resp.StatusCode)
	}
}
