package server

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

const requestIDHeader = "X-Request-ID"

// responseWriter wraps http.ResponseWriter to capture the status code and the
// number of bytes written for access logging.
type responseWriter struct {
	http.ResponseWriter

	status int
	bytes  int
}

func wrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w, status: http.StatusOK}
}

func (w *responseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// newRequestID returns a short random hex identifier without pulling in an
// external UUID dependency.
func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf[:])
}

// LoggingMiddleware assigns a request ID, echoes it back in a header, and logs
// request completion at a level chosen by the response status.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := newRequestID()

		ww := wrapResponseWriter(w)
		ww.Header().Set(requestIDHeader, requestID)
		next.ServeHTTP(ww, r)

		attrs := []any{
			slog.String("request_id", requestID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", ww.status),
			slog.Int("bytes", ww.bytes),
			slog.String("remote_addr", r.RemoteAddr),
			slog.Duration("duration", time.Since(start)),
		}
		switch {
		case ww.status >= http.StatusInternalServerError:
			slog.Error("request completed", attrs...)
		case ww.status >= http.StatusBadRequest:
			slog.Warn("request completed", attrs...)
		default:
			slog.Info("request completed", attrs...)
		}
	})
}

// RecoverMiddleware converts a handler panic into a 500 response and logs the
// stack trace. It must wrap the logging middleware so panics there are caught
// too.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered",
					"error", rec,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
