package mattermost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"short unchanged", "hello", 16383, "hello"},
		{"exact fit", "abc", 3, "abc"},
		{"cut ascii", "abcdef", 3, "ab…"},
		{"cut cyrillic", "привет мир", 5, "прив…"},
		{"emoji boundary", "🙂🙂🙂🙂", 2, "🙂…"},
		{"limit one", "abc", 1, "a"},
		{"limit zero", "abc", 0, ""},
		{"negative limit", "abc", -1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.in, tt.limit)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
			if tt.limit >= 0 && utf8.RuneCountInString(got) > tt.limit {
				t.Errorf("result %q exceeds rune limit %d", got, tt.limit)
			}
		})
	}
}

func TestPost(t *testing.T) {
	var gotChannel, gotMessage, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/posts" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		var post struct {
			ChannelID string `json:"channel_id"`
			Message   string `json:"message"`
		}
		if !decodeBody(t, w, r, &post) {
			return
		}
		gotChannel = post.ChannelID
		gotMessage = post.Message
		writeBody(t, w, http.StatusCreated, `{"id":"post1"}`)
	}))
	defer ts.Close()

	c := mustClient(t, ts.URL, "secret-token")
	if err := c.Post(context.Background(), "chan42", "hello world"); err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	if gotChannel != "chan42" {
		t.Errorf("channel = %q, want chan42", gotChannel)
	}
	if gotMessage != "hello world" {
		t.Errorf("message = %q, want hello world", gotMessage)
	}
	if !strings.EqualFold(gotAuth, "Bearer secret-token") {
		t.Errorf("auth = %q, want Bearer secret-token (case-insensitive)", gotAuth)
	}
}

// TestPostTruncatesToConfiguredLimit proves the rune limit passed to New reaches
// the wire, instead of the package default.
func TestPostTruncatesToConfiguredLimit(t *testing.T) {
	var gotMessage string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var post struct {
			Message string `json:"message"`
		}
		if !decodeBody(t, w, r, &post) {
			return
		}
		gotMessage = post.Message
		writeBody(t, w, http.StatusCreated, `{"id":"post1"}`)
	}))
	defer ts.Close()

	const maxRunes = 5
	c, err := New(ts.URL, "token", 5*time.Second, maxRunes)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	// Cyrillic input also checks that the cut is by runes, not bytes.
	if err = c.Post(context.Background(), "chan", "привет мир"); err != nil {
		t.Fatalf("Post failed: %v", err)
	}
	if gotMessage != "прив…" {
		t.Errorf("message = %q, want %q", gotMessage, "прив…")
	}
	if n := utf8.RuneCountInString(gotMessage); n > maxRunes {
		t.Errorf("message has %d runes, want at most %d", n, maxRunes)
	}
}

func TestPostServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"boom"}`, http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := mustClient(t, ts.URL, "token")
	err := c.Post(context.Background(), "chan", "msg")
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "create post") {
		t.Errorf("error = %q, want it to mention create post", err.Error())
	}
}

func TestPing(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v4/users/me" {
			http.Error(w, "unexpected", http.StatusNotFound)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		writeBody(t, w, http.StatusOK, `{"id":"user1","username":"bot"}`)
	}))
	defer ts.Close()

	c := mustClient(t, ts.URL, "secret-token")
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("authorization = %q, want bearer token", gotAuth)
	}
}

func TestPingRejectsMalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, http.StatusOK, `{broken`)
	}))
	defer ts.Close()

	err := mustClient(t, ts.URL, "token").Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "decode JSON") {
		t.Fatalf("Ping error = %v, want JSON decode error", err)
	}
}

func TestAPIErrorDoesNotExposeToken(t *testing.T) {
	const token = "very-secret-token"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeBody(t, w, http.StatusUnauthorized, `{"message":"invalid credentials"}`)
	}))
	defer ts.Close()

	err := mustClient(t, ts.URL, token).Post(context.Background(), "chan", "message")
	if err == nil {
		t.Fatal("expected API error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error exposes token: %v", err)
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid credentials") {
		t.Errorf("error = %q, want status and API message", err)
	}
}

func TestNewRejectsInvalidURL(t *testing.T) {
	for _, rawURL := range []string{"https:", "ftp://mm.example.com"} {
		if _, err := New(rawURL, "token", time.Second, defaultMaxMessageRunes); err == nil {
			t.Errorf("New(%q) succeeded, want error", rawURL)
		}
	}
}

func TestNewFallsBackToDefaultTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		c, err := New("https://mm.example.com", "token", timeout, defaultMaxMessageRunes)
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}
		if c.timeout != defaultTimeout {
			t.Errorf("New(..., %v) timeout = %v, want default %v", timeout, c.timeout, defaultTimeout)
		}
	}
}

func TestNewFallsBackToDefaultMaxRunes(t *testing.T) {
	for _, maxRunes := range []int{0, -1} {
		c, err := New("https://mm.example.com", "token", time.Second, maxRunes)
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}
		if c.maxRunes != defaultMaxMessageRunes {
			t.Errorf("New(..., %d) maxRunes = %d, want default %d", maxRunes, c.maxRunes, defaultMaxMessageRunes)
		}
	}
}

// TestPostHonorsTimeout proves the configured timeout reaches the request
// context. The handler withholds its response until the test releases it, which
// happens once the client has already given up.
func TestPostHonorsTimeout(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	// Defers run last-in-first-out: unblock the handler before Close waits on it.
	defer ts.Close()
	defer close(release)

	c, err := New(ts.URL, "token", 100*time.Millisecond, defaultMaxMessageRunes)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	err = c.Post(context.Background(), "chan", "msg")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
}

// decodeBody reads and unmarshals a request captured by a test handler, and
// reports false once it has answered the client itself. Without it a broken body
// surfaces as an empty captured field ("channel = \"\", want chan42") instead of
// the read or unmarshal error that actually caused it.
//
// It reports failures with Errorf, never Fatalf: this runs in the httptest server
// goroutine, and FailNow must be called from the goroutine running the test. The
// handler is driven synchronously from inside the client call under test, so the
// test cannot have finished by the time this logs.
func decodeBody(t *testing.T, w http.ResponseWriter, r *http.Request, dst any) bool {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read request body: %v", err)
		http.Error(w, "read body", http.StatusInternalServerError)
		return false
	}
	if err = json.Unmarshal(body, dst); err != nil {
		t.Errorf("unmarshal request body %q: %v", body, err)
		http.Error(w, "bad body", http.StatusInternalServerError)
		return false
	}
	return true
}

// writeBody writes a canned API response. A failed write means the client hung up
// mid-test; reporting it here keeps that from reaching the test as a confusing
// decode error. Same goroutine caveat as decodeBody.
func writeBody(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write response body: %v", err)
	}
}

func mustClient(t *testing.T, baseURL, token string) *Client {
	t.Helper()
	c, err := New(baseURL, token, 5*time.Second, defaultMaxMessageRunes)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return c
}
