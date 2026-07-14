package server

import (
	"net/http"
	"net/http/httptest"
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
