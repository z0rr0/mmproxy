package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverMiddleware(t *testing.T) {
	panicky := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	handler := RecoverMiddleware(LoggingMiddleware(panicky))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestLoggingMiddlewareRequestID(t *testing.T) {
	handler := LoggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Header().Get(requestIDHeader) == "" {
		t.Error("missing X-Request-ID header")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418 (passed through)", rec.Code)
	}
}

func TestResponseWriterCaptures(t *testing.T) {
	rec := httptest.NewRecorder()
	ww := wrapResponseWriter(rec)
	ww.WriteHeader(http.StatusCreated)
	n, err := ww.Write([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if ww.status != http.StatusCreated {
		t.Errorf("status = %d, want 201", ww.status)
	}
	if ww.bytes != n || ww.bytes != 5 {
		t.Errorf("bytes = %d, want 5", ww.bytes)
	}
}

func TestResponseWriterKeepsFirstStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	ww := wrapResponseWriter(rec)
	ww.WriteHeader(http.StatusCreated)
	ww.WriteHeader(http.StatusInternalServerError)

	if rec.Code != http.StatusCreated || ww.status != http.StatusCreated {
		t.Fatalf("status recorder=%d wrapper=%d, want first status 201", rec.Code, ww.status)
	}
}

func TestResponseWriterImplicitOK(t *testing.T) {
	rec := httptest.NewRecorder()
	ww := wrapResponseWriter(rec)
	if _, err := ww.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if ww.status != http.StatusOK || !ww.wroteHeader {
		t.Fatalf("status=%d wroteHeader=%v, want explicit captured 200", ww.status, ww.wroteHeader)
	}
}

func TestLoggingMiddlewareLogsRecoveredPanic(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	panicky := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	handler := RecoverMiddleware(LoggingMiddleware(panicky))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	for _, want := range []string{"panic recovered", "request completed", "status=500"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("logs missing %q: %s", want, logs.String())
		}
	}
}

func TestLoggingMiddlewarePanicAfterHeaderKeepsStatus(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	panicky := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		panic("boom")
	})
	rec := httptest.NewRecorder()
	LoggingMiddleware(panicky).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want committed status 202", rec.Code)
	}
	if !strings.Contains(logs.String(), "status=202") {
		t.Errorf("logs missing committed status: %s", logs.String())
	}
}
