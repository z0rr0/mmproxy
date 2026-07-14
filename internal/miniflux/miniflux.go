// Package miniflux contains the minimal webhook payload types, HMAC signature
// verification and Mattermost post formatting for the Miniflux integration.
//
// The types intentionally mirror only the fields MMProxy needs. The upstream
// miniflux.app/v2/client models describe the REST API, not the webhook payload
// (e.g. the webhook feed carries feed_url/site_url), so reusing them is unsafe.
package miniflux

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// EventNewEntries is the only webhook event MMProxy acts on. Other events
// (e.g. "save_entry") are acknowledged and ignored.
const EventNewEntries = "new_entries"

// WebhookEvent is the top-level Miniflux webhook payload.
type WebhookEvent struct {
	EventType string  `json:"event_type"`
	Feed      Feed    `json:"feed"`
	Entries   []Entry `json:"entries"`
}

// Feed is the minimal feed description from a webhook payload.
type Feed struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	SiteURL string `json:"site_url"`
}

// Entry is a single feed entry from a webhook payload.
type Entry struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ValidSignature reports whether headerHex is the correct HMAC-SHA256 of body
// keyed by secret. Comparison is constant-time. An empty or non-hex header
// yields false.
func ValidSignature(secret, body []byte, headerHex string) bool {
	want, err := hex.DecodeString(headerHex)
	if err != nil || len(want) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(want, mac.Sum(nil))
}

// FormatPost renders all entries of an event into a single Markdown post.
// Mattermost renders Markdown, so entries become a bulleted list of links.
func FormatPost(event *WebhookEvent) string {
	var b strings.Builder
	if event.Feed.Title != "" {
		b.WriteString("#### ")
		b.WriteString(event.Feed.Title)
		b.WriteString("\n")
	}
	for _, e := range event.Entries {
		title := e.Title
		if title == "" {
			title = e.URL
		}
		b.WriteString("- [")
		b.WriteString(title)
		b.WriteString("](")
		b.WriteString(e.URL)
		b.WriteString(")\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
