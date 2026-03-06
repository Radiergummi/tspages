package ratelimit

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"tspages/internal/auth"
)

// Tier identifies which rate limit bucket applies.
type Tier int

const (
	Mutate Tier = iota // heavy mutations (deploy, delete, config changes)
	Submit             // per-site form submissions
	Query              // expensive reads (analytics, CSV export)
)

// TierConfig holds the rate and burst for a single tier.
type TierConfig struct {
	Rate  float64
	Burst int
}

type tierKey struct {
	key  string
	tier Tier
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// Limiter is a per-user, per-tier token bucket rate limiter.
// A nil *Limiter is safe to use — all methods return "allow".
type Limiter struct {
	tiers [3]TierConfig

	mu      sync.Mutex
	entries map[tierKey]*entry
}

// New creates a Limiter and starts a background goroutine that
// evicts idle entries every 5 minutes.
func New(mutate, submit, query TierConfig) *Limiter {
	l := &Limiter{
		tiers:   [3]TierConfig{mutate, submit, query},
		entries: make(map[tierKey]*entry),
	}
	go l.cleanup()
	return l
}

// Allow reports whether the request identified by key is allowed under
// the given tier. A nil Limiter or a tier with Rate==0 always allows.
func (l *Limiter) Allow(key string, tier Tier) bool {
	if l == nil {
		return true
	}
	cfg := l.tiers[tier]
	if cfg.Rate == 0 {
		return true
	}

	k := tierKey{key: key, tier: tier}
	l.mu.Lock()
	e, ok := l.entries[k]
	if !ok {
		e = &entry{limiter: rate.NewLimiter(rate.Limit(cfg.Rate), cfg.Burst)}
		l.entries[k] = e
	}
	e.lastSeen = time.Now()
	l.mu.Unlock()

	return e.limiter.Allow()
}

// DenyFunc writes the 429 response body. The Retry-After header is
// already set before this is called.
type DenyFunc func(w http.ResponseWriter, r *http.Request)

// Middleware returns HTTP middleware that rejects requests over the
// tier's limit with 429 Too Many Requests. A nil Limiter passes through.
// If onDeny is nil, a plain-text "rate limit exceeded" response is sent.
func Middleware(l *Limiter, tier Tier, onDeny DenyFunc) func(http.Handler) http.Handler {
	if onDeny == nil {
		onDeny = func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		}
	}
	return func(next http.Handler) http.Handler {
		if l == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(RequestKey(r), tier) {
				w.Header().Set("Retry-After", "1")
				onDeny(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequestKey extracts a rate-limit key from r. It uses the authenticated
// user's login name if available, otherwise the remote IP address.
func RequestKey(r *http.Request) string {
	if ri := auth.RequestInfoFromContext(r.Context()); ri.UserLogin != "" {
		return ri.UserLogin
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// cleanup runs sweep every 5 minutes until the process exits.
func (l *Limiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.sweep()
	}
}

// sweep removes entries that haven't been seen in 10 minutes.
func (l *Limiter) sweep() {
	cutoff := time.Now().Add(-10 * time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, e := range l.entries {
		if e.lastSeen.Before(cutoff) {
			delete(l.entries, k)
		}
	}
}
