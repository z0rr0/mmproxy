package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

const (
	requestIDHeader = "X-Request-ID"
	// maxRequestIDLen caps a client-supplied request ID; ours is 16 hex chars.
	maxRequestIDLen = 64
)

// contextKey is the private key type for values this package stores in a
// request context, so the keys cannot collide with other packages.
type contextKey int

const requestIDKey contextKey = iota

// withRequestID stores the request ID for handlers that log below the middleware.
func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// requestIDFrom returns the request ID stored by LoggingMiddleware, or an empty
// string when a handler runs without it (a unit test calling it directly).
func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// responseWriter wraps http.ResponseWriter to capture the status code and the
// number of bytes written for access logging.
type responseWriter struct {
	http.ResponseWriter

	status      int
	bytes       int
	wroteHeader bool
}

func wrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w, status: http.StatusOK}
}

func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap allows http.ResponseController to reach optional interfaces provided
// by the underlying writer.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// newRequestID returns a short random hex identifier without pulling in an
// external UUID dependency.
func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf[:])
}

// sanitizeRequestID accepts a client-supplied request ID only when it is a short
// token of unambiguous characters, and returns an empty string otherwise. The
// value is echoed back in a response header and written to logs, so nothing else
// may pass through.
func sanitizeRequestID(v string) string {
	if v == "" || len(v) > maxRequestIDLen {
		return ""
	}
	for i := range len(v) {
		switch c := v[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return ""
		}
	}
	return v
}

// LoggingMiddleware assigns a request ID, echoes it back in a header, stores it
// in the request context for the handlers, and logs request completion at a level
// chosen by the response status. A valid incoming X-Request-ID is reused so a
// trace survives across a reverse proxy.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := sanitizeRequestID(r.Header.Get(requestIDHeader))
		if requestID == "" {
			requestID = newRequestID()
		}
		r = r.WithContext(withRequestID(r.Context(), requestID))

		ww := wrapResponseWriter(w)
		ww.Header().Set(requestIDHeader, requestID)
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic recovered",
					"error", rec,
					"request_id", requestID,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)
				if !ww.wroteHeader {
					http.Error(ww, "Internal Server Error", http.StatusInternalServerError)
				}
			}
			logRequestCompleted(r, ww, requestID, time.Since(start))
		}()

		next.ServeHTTP(ww, r)
	})
}

func logRequestCompleted(r *http.Request, w *responseWriter, requestID string, duration time.Duration) {
	attrs := []any{
		slog.String("request_id", requestID),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", w.status),
		slog.Int("bytes", w.bytes),
		slog.String("remote_addr", r.RemoteAddr),
		slog.Duration("duration", duration),
	}
	switch {
	case w.status >= http.StatusInternalServerError:
		slog.Error("request completed", attrs...)
	case w.status >= http.StatusBadRequest:
		slog.Warn("request completed", attrs...)
	default:
		slog.Info("request completed", attrs...)
	}
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
