package mattermost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		body, _ := io.ReadAll(r.Body)
		var post struct {
			ChannelID string `json:"channel_id"`
			Message   string `json:"message"`
		}
		_ = json.Unmarshal(body, &post)
		gotChannel = post.ChannelID
		gotMessage = post.Message
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"post1"}`)
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
		_, _ = io.WriteString(w, `{"id":"user1","username":"bot"}`)
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
		_, _ = io.WriteString(w, `{broken`)
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
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"invalid credentials"}`)
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
		if _, err := New(rawURL, "token"); err == nil {
			t.Errorf("New(%q) succeeded, want error", rawURL)
		}
	}
}

func mustClient(t *testing.T, baseURL, token string) *Client {
	t.Helper()
	c, err := New(baseURL, token)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return c
}
