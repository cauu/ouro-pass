// Package middleware holds the cross-cutting HTTP middleware: structured
// request logging, per-IP rate limiting (public/verifier planes), and
// Idempotency-Key replay for create endpoints (detailed §9.1). chi's built-in
// RequestID/Recoverer are composed alongside these in the router.
package middleware

import (
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"ouro-pass/server/internal/httpapi/respond"
	"golang.org/x/time/rate"
)

// statusRecorder captures the response status (and body, for idempotency).
type statusRecorder struct {
	http.ResponseWriter
	status  int
	body    *bytes.Buffer
	capture bool
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.capture {
		r.body.Write(b)
	}
	return r.ResponseWriter.Write(b)
}

// RequestLogger logs one structured line per request with method, path, status,
// duration, and the chi request id.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"dur_ms", time.Since(start).Milliseconds(),
			"req_id", middleware.GetReqID(r.Context()),
		)
	})
}

// IPRateLimiter is a per-client-IP token-bucket limiter for the public and
// verifier planes (detailed §9.1). Idle limiters are garbage-collected.
type IPRateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*clientLimiter
	rps      rate.Limit
	burst    int
	ttl      time.Duration
}

type clientLimiter struct {
	limiter *rate.Limiter
	lastSeen time.Time
}

// NewIPRateLimiter builds a limiter allowing rps requests/sec with the given
// burst per IP.
func NewIPRateLimiter(rps float64, burst int) *IPRateLimiter {
	l := &IPRateLimiter{
		clients: make(map[string]*clientLimiter),
		rps:     rate.Limit(rps),
		burst:   burst,
		ttl:     10 * time.Minute,
	}
	return l
}

func (l *IPRateLimiter) limiterFor(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.clients[ip]
	if !ok {
		c = &clientLimiter{limiter: rate.NewLimiter(l.rps, l.burst)}
		l.clients[ip] = c
	}
	c.lastSeen = time.Now()
	// Opportunistic eviction of stale entries.
	for k, v := range l.clients {
		if time.Since(v.lastSeen) > l.ttl {
			delete(l.clients, k)
		}
	}
	return c.limiter
}

// Middleware returns the http middleware enforcing the limit; over-limit
// requests get 429 with the OAuth error envelope.
func (l *IPRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.limiterFor(clientIP(r)).Allow() {
			respond.Error(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Idempotency replays the first response for a given Idempotency-Key on create
// endpoints (detailed §9.1). MVP store is in-memory with TTL; a key seen again
// within TTL returns the cached status+body without re-running the handler.
type Idempotency struct {
	mu    sync.Mutex
	cache map[string]idempotentResponse
	ttl   time.Duration
}

type idempotentResponse struct {
	status  int
	body    []byte
	savedAt time.Time
}

// NewIdempotency builds an in-memory idempotency cache.
func NewIdempotency(ttl time.Duration) *Idempotency {
	return &Idempotency{cache: make(map[string]idempotentResponse), ttl: ttl}
}

// Middleware applies idempotent replay when an Idempotency-Key header is
// present. Without the header, requests pass through unchanged.
func (i *Idempotency) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		// Scope the key to method+path so the same Idempotency-Key reused across
		// endpoints can't replay the wrong endpoint's body (p12-7).
		scoped := r.Method + " " + r.URL.Path + "\x00" + key
		if resp, ok := i.get(scoped); ok {
			w.Header().Set("Idempotency-Replayed", "true")
			respondRaw(w, resp.status, resp.body)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, body: &bytes.Buffer{}, capture: true}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		// Only cache success: a transient 4xx/5xx must not be pinned and replayed
		// for the whole TTL, which would block legitimate retries (p12-7).
		if rec.status >= 200 && rec.status < 300 {
			i.put(scoped, rec.status, rec.body.Bytes())
		}
	})
}

func (i *Idempotency) get(key string) (idempotentResponse, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	r, ok := i.cache[key]
	if !ok || time.Since(r.savedAt) > i.ttl {
		if ok {
			delete(i.cache, key)
		}
		return idempotentResponse{}, false
	}
	return r, true
}

func (i *Idempotency) put(key string, status int, body []byte) {
	i.mu.Lock()
	defer i.mu.Unlock()
	// Opportunistically evict expired entries so a stream of unique keys can't
	// grow the map without bound (memory-exhaustion DoS, p12-7) — same approach
	// as the IP limiter's sweep.
	now := time.Now()
	for k, v := range i.cache {
		if now.Sub(v.savedAt) > i.ttl {
			delete(i.cache, k)
		}
	}
	cp := make([]byte, len(body))
	copy(cp, body)
	i.cache[key] = idempotentResponse{status: status, body: cp, savedAt: now}
}

func respondRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
