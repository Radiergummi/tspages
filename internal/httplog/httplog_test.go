package httplog

import (
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
