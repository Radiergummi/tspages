package httplog

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusRecorder_Default200(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: 200}
	rec.Write([]byte("ok"))
	if rec.status != 200 {
		t.Errorf("status = %d, want 200", rec.status)
	}
}

func TestStatusRecorder_ExplicitStatus(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: 200}
	rec.WriteHeader(http.StatusNotFound)
	if rec.status != 404 {
		t.Errorf("status = %d, want 404", rec.status)
	}
}

func TestWrap_CapturesStatus(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	h := Wrap(inner)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("response status = %d, want 404", rec.Code)
	}
}

func TestWrap_WithAttrs(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Must not panic when extra attrs are passed (used by multihost).
	h := Wrap(inner, slog.String("site", "docs"), slog.String("extra", "val"))

	req := httptest.NewRequest("GET", "/page", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("response status = %d, want 200", rec.Code)
	}
}
