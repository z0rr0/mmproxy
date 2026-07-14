package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/z0rr0/mmproxy/internal/miniflux"
)

// maxBodyBytes caps the Miniflux webhook body. Miniflux embeds each entry's HTML
// content, so a batch can be large; 4 MiB is a generous ceiling.
const maxBodyBytes = 4 << 20

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeText(w, http.StatusOK, "OK")
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeText(w, http.StatusOK, s.version)
}

// handleMiniflux verifies the HMAC signature over the raw body, then parses and
// forwards new-entry events to Mattermost. The read/verify/parse order is
// deliberate: the signature is computed over the exact bytes received, before
// any JSON decoding, and rejected input never reaches the parser.
func (s *Server) handleMiniflux(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeText(w, http.StatusRequestEntityTooLarge, "body too large")
			return
		}
		writeText(w, http.StatusBadRequest, "cannot read body")
		return
	}

	sig := r.Header.Get("X-Miniflux-Signature")
	if !miniflux.ValidSignature(s.secret, body, sig) {
		writeText(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var event miniflux.WebhookEvent
	if err = json.Unmarshal(body, &event); err != nil {
		writeText(w, http.StatusBadRequest, "invalid json")
		return
	}

	if event.EventType != miniflux.EventNewEntries {
		slog.Info("miniflux event ignored", "event_type", event.EventType)
		writeText(w, http.StatusOK, "ignored")
		return
	}

	if !s.feedAllowed(event.Feed.ID) {
		slog.Info("miniflux feed filtered", "feed_id", event.Feed.ID, "feed_title", event.Feed.Title)
		writeText(w, http.StatusOK, "filtered")
		return
	}

	if len(event.Entries) == 0 {
		writeText(w, http.StatusOK, "no entries")
		return
	}

	message := miniflux.FormatPost(&event)
	if err = s.poster.Post(r.Context(), s.miniflux.ChannelID, message); err != nil {
		slog.Error("miniflux post failed", "error", err, "feed_id", event.Feed.ID)
		writeText(w, http.StatusBadGateway, "upstream error")
		return
	}
	writeText(w, http.StatusOK, "OK")
}

// feedAllowed reports whether the feed passes the configured allowlist. An empty
// allowlist accepts every feed.
func (s *Server) feedAllowed(feedID int64) bool {
	if len(s.miniflux.AllowedFeeds) == 0 {
		return true
	}
	_, ok := s.miniflux.AllowedFeeds[feedID]
	return ok
}

func writeText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, msg)
}
