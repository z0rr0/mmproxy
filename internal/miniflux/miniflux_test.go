package miniflux

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestValidSignature(t *testing.T) {
	secret := []byte("topsecret")
	body := []byte(`{"event_type":"new_entries"}`)
	good := sign("topsecret", string(body))

	tests := []struct {
		name   string
		header string
		want   bool
	}{
		{"valid", good, true},
		{"wrong secret", sign("other", string(body)), false},
		{"empty header", "", false},
		{"non-hex header", "zzzz", false},
		{"truncated", good[:len(good)-2], false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidSignature(secret, body, tt.header); got != tt.want {
				t.Errorf("ValidSignature(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestFormatPost(t *testing.T) {
	tests := []struct {
		name  string
		event WebhookEvent
		want  string
	}{
		{
			name: "single entry",
			event: WebhookEvent{
				Feed:    Feed{Title: "News"},
				Entries: []Entry{{Title: "Hello", URL: "https://e.com/1"}},
			},
			want: "#### News\n- [Hello](https://e.com/1)",
		},
		{
			name: "multiple entries in one post",
			event: WebhookEvent{
				Feed: Feed{Title: "News"},
				Entries: []Entry{
					{Title: "A", URL: "https://e.com/a"},
					{Title: "B", URL: "https://e.com/b"},
				},
			},
			want: "#### News\n- [A](https://e.com/a)\n- [B](https://e.com/b)",
		},
		{
			name: "empty title falls back to url",
			event: WebhookEvent{
				Feed:    Feed{Title: "News"},
				Entries: []Entry{{Title: "", URL: "https://e.com/x"}},
			},
			want: "#### News\n- [https://e.com/x](https://e.com/x)",
		},
		{
			name: "no feed title",
			event: WebhookEvent{
				Entries: []Entry{{Title: "Solo", URL: "https://e.com/s"}},
			},
			want: "- [Solo](https://e.com/s)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatPost(&tt.event); got != tt.want {
				t.Errorf("FormatPost() = %q, want %q", got, tt.want)
			}
		})
	}
}
