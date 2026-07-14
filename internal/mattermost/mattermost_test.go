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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.in, tt.limit)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}
			if utf8.RuneCountInString(got) > tt.limit {
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

	c := New(ts.URL, "secret-token")
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

	c := New(ts.URL, "token")
	err := c.Post(context.Background(), "chan", "msg")
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "create post") {
		t.Errorf("error = %q, want it to mention create post", err.Error())
	}
}
