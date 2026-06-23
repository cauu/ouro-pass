package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIPRateLimiter_BlocksOverBurst(t *testing.T) {
	rl := NewIPRateLimiter(1, 2) // 1 rps, burst 2
	h := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	call := func() int {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if call() != 200 || call() != 200 {
		t.Fatal("first two (burst) should pass")
	}
	if got := call(); got != http.StatusTooManyRequests {
		t.Fatalf("third call = %d, want 429", got)
	}

	// A different IP has its own bucket.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.2:1"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("other IP = %d, want 200", rr.Code)
	}
}

func TestIdempotency_ReplaysResponse(t *testing.T) {
	idem := NewIdempotency(time.Minute)
	calls := 0
	h := idem.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"n":` + itoa(calls) + `}`))
	}))

	do := func(key string) (int, string, string) {
		req := httptest.NewRequest(http.MethodPost, "/create", nil)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		body, _ := io.ReadAll(rr.Result().Body)
		return rr.Code, string(body), rr.Header().Get("Idempotency-Replayed")
	}

	c1, b1, r1 := do("K1")
	if c1 != 201 || b1 != `{"n":1}` || r1 == "true" {
		t.Fatalf("first: %d %q replayed=%q", c1, b1, r1)
	}
	c2, b2, r2 := do("K1")
	if c2 != 201 || b2 != `{"n":1}` || r2 != "true" {
		t.Fatalf("replay: %d %q replayed=%q (handler ran %d times)", c2, b2, r2, calls)
	}
	if calls != 1 {
		t.Fatalf("handler ran %d times, want 1 (idempotent)", calls)
	}
	// No key → always runs.
	c3, b3, _ := do("")
	if c3 != 201 || b3 != `{"n":2}` {
		t.Fatalf("no-key: %d %q", c3, b3)
	}
}

func TestRequestLogger_PassesThrough(t *testing.T) {
	h := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rr.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", rr.Code)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
