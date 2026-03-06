package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tspages/internal/auth"
)

func TestAllow_NilLimiter(t *testing.T) {
	var l *Limiter
	if !l.Allow("user@example.com", Mutate) {
		t.Fatal("nil limiter should always allow")
	}
}

func TestAllow_ZeroRate(t *testing.T) {
	l := New(TierConfig{Rate: 0, Burst: 0}, TierConfig{}, TierConfig{})
	for range 100 {
		if !l.Allow("user@example.com", Mutate) {
			t.Fatal("zero rate should always allow")
		}
	}
}

func TestAllow_BurstThenDeny(t *testing.T) {
	l := New(TierConfig{Rate: 1, Burst: 2}, TierConfig{}, TierConfig{})
	if !l.Allow("u", Mutate) {
		t.Fatal("first request should be allowed")
	}
	if !l.Allow("u", Mutate) {
		t.Fatal("second request should be allowed (burst=2)")
	}
	if l.Allow("u", Mutate) {
		t.Fatal("third request should be denied")
	}
}

func TestAllow_PerUser(t *testing.T) {
	l := New(TierConfig{Rate: 1, Burst: 1}, TierConfig{}, TierConfig{})
	if !l.Allow("alice", Mutate) {
		t.Fatal("alice's first request should be allowed")
	}
	if !l.Allow("bob", Mutate) {
		t.Fatal("bob's first request should be allowed (independent bucket)")
	}
}

func TestMiddleware_Allows(t *testing.T) {
	l := New(TierConfig{Rate: 10, Burst: 10}, TierConfig{}, TierConfig{})
	handler := Middleware(l, Mutate, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("POST", "/deploy/test", nil)
	r.RemoteAddr = "100.64.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestMiddleware_Denies(t *testing.T) {
	l := New(TierConfig{Rate: 1, Burst: 1}, TierConfig{}, TierConfig{})
	handler := Middleware(l, Mutate, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("POST", "/deploy/test", nil)
	r.RemoteAddr = "100.64.0.1:12345"

	// First: allowed.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Second: denied.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") != "1" {
		t.Fatalf("expected Retry-After: 1, got %q", w.Header().Get("Retry-After"))
	}
}

func TestMiddleware_NilLimiter(t *testing.T) {
	handler := Middleware(nil, Mutate, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("POST", "/deploy/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequestKey_Authenticated(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	ctx := auth.ContextWithRequestInfo(r.Context(), auth.RequestInfo{UserLogin: "alice@example.com"})
	r = r.WithContext(ctx)

	if got := RequestKey(r); got != "alice@example.com" {
		t.Fatalf("expected alice@example.com, got %q", got)
	}
}

func TestRequestKey_Anonymous(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.1:54321"

	if got := RequestKey(r); got != "192.168.1.1" {
		t.Fatalf("expected 192.168.1.1, got %q", got)
	}
}

func TestRequestKey_NoPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.1" // no port — SplitHostPort will fail

	if got := RequestKey(r); got != "192.168.1.1" {
		t.Fatalf("expected 192.168.1.1, got %q", got)
	}
}

func TestMiddleware_CustomDenyFunc(t *testing.T) {
	l := New(TierConfig{Rate: 1, Burst: 1}, TierConfig{}, TierConfig{})
	deny := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"slow down"}`))
	}
	handler := Middleware(l, Mutate, deny)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("POST", "/deploy/test", nil)
	r.RemoteAddr = "100.64.0.1:12345"

	// Exhaust burst.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	// Denied — should use custom deny func.
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") != "1" {
		t.Fatalf("expected Retry-After: 1, got %q", w.Header().Get("Retry-After"))
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	if body := w.Body.String(); body != `{"error":"slow down"}` {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestMiddleware_ZeroRatePassesThrough(t *testing.T) {
	l := New(TierConfig{Rate: 0, Burst: 0}, TierConfig{}, TierConfig{})
	called := 0
	handler := Middleware(l, Mutate, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("POST", "/deploy/test", nil)
	r.RemoteAddr = "100.64.0.1:12345"
	for range 50 {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	}
	if called != 50 {
		t.Fatalf("expected handler called 50 times, got %d", called)
	}
}

func TestSweep(t *testing.T) {
	l := New(TierConfig{Rate: 10, Burst: 10}, TierConfig{}, TierConfig{})
	l.Allow("fresh", Mutate)
	l.Allow("stale", Mutate)

	// Backdate the stale entry.
	k := tierKey{key: "stale", tier: Mutate}
	l.mu.Lock()
	l.entries[k].lastSeen = time.Now().Add(-15 * time.Minute)
	l.mu.Unlock()

	l.sweep()

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.entries[k]; ok {
		t.Fatal("stale entry should have been swept")
	}
	if _, ok := l.entries[tierKey{key: "fresh", tier: Mutate}]; !ok {
		t.Fatal("fresh entry should still exist")
	}
}

func TestAllow_DifferentTiers(t *testing.T) {
	l := New(
		TierConfig{Rate: 1, Burst: 1},
		TierConfig{Rate: 1, Burst: 1},
		TierConfig{},
	)
	// Exhaust mutate tier.
	l.Allow("u", Mutate)
	if l.Allow("u", Mutate) {
		t.Fatal("mutate should be exhausted")
	}
	// Submit tier should still allow — independent bucket.
	if !l.Allow("u", Submit) {
		t.Fatal("submit should be allowed (independent tier)")
	}
}
