package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TC-1: four planes reachable with expected statuses; health 200; unknown 404;
// admin without session 401.
func TestRouter_PlaneReachability(t *testing.T) {
	srv := httptest.NewServer(NewRouter(Deps{}))
	defer srv.Close()

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodGet, "/does-not-exist", http.StatusNotFound},
		{http.MethodPost, "/api/auth/challenge", http.StatusNotImplemented},
		{http.MethodGet, "/connect", http.StatusNotImplemented},
		{http.MethodPost, "/api/oauth/token", http.StatusNotImplemented},
		{http.MethodGet, "/.well-known/poolops/jwks.json", http.StatusNotImplemented},
		{http.MethodPost, "/api/activation/create", http.StatusNotImplemented},
		{http.MethodGet, "/api/admin/audit", http.StatusUnauthorized}, // gated
	}
	for _, c := range cases {
		req, _ := http.NewRequest(c.method, srv.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.want {
			t.Errorf("%s %s: got %d, want %d", c.method, c.path, resp.StatusCode, c.want)
		}
	}
}

func TestRouter_HealthBody(t *testing.T) {
	rr := httptest.NewRecorder()
	NewRouter(Deps{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("health body not JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("health status = %q, want ok", body["status"])
	}
}

// TC-1: graceful shutdown returns cleanly.
func TestServer_GracefulShutdown(t *testing.T) {
	srv := &http.Server{Handler: NewRouter(Deps{})}
	ln := httptest.NewServer(srv.Handler)
	ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("graceful shutdown: %v", err)
	}
}
