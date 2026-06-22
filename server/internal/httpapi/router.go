// Package httpapi assembles the chi router. The route groups map 1:1 to the
// authentication planes in the detailed design §9.1 — wallet primitive, OAuth
// issuance, channel activation, verifier, and admin — so the attack surface is
// visible in the routing tree.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Deps carries the collaborators the handlers need. Fields are added as later
// plan items wire in real implementations; p1-1 only needs the router shape.
type Deps struct {
	// reserved for stores, services, clock, etc.
}

// NewRouter builds the full route tree. Handlers not yet implemented respond
// 501; admin routes without a session respond 401 (see TC-1).
func NewRouter(_ Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// Liveness — unauthenticated, always cheap.
	r.Get("/healthz", health)

	// ---- Wallet primitive plane (no auth; issues nonces) ----
	r.Post("/api/auth/challenge", notImplemented)

	// ---- Issuance (OAuth) plane ----
	r.Get("/connect", notImplemented)
	r.Post("/api/connect/authorize", notImplemented)
	r.Post("/api/oauth/token", notImplemented)

	// ---- Channel activation plane (wallet signature) ----
	r.Post("/api/activation/create", notImplemented)

	// ---- Verifier plane (public, read-only, rate-limited) ----
	r.Get("/.well-known/poolops/jwks.json", notImplemented)
	r.Post("/api/oauth/introspect", notImplemented)
	r.Post("/api/oauth/revoke", notImplemented)

	// ---- Admin plane (owner-key session + RBAC + step-up) ----
	r.Route("/api/admin", func(r chi.Router) {
		// Unauthenticated login primitives.
		r.Post("/auth/challenge", notImplemented)
		r.Post("/auth/verify", notImplemented)
		// Everything else requires a session; stub gate returns 401 until p8.
		r.Group(func(r chi.Router) {
			r.Use(requireAdminSession)
			r.Get("/audit", notImplemented)
		})
	})

	return r
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "endpoint not yet implemented")
}

// requireAdminSession is a placeholder gate replaced by the real session +
// RBAC middleware in p8-1. Until then it denies access so the admin plane is
// observably protected (TC-1).
func requireAdminSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "admin session required")
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError emits the OAuth-style error envelope used across all planes
// (detailed design §9.1): {"error":"code","error_description":"..."}.
func writeError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}
