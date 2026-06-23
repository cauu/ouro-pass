// Package httpapi assembles the chi router. The route groups map 1:1 to the
// authentication planes in the detailed design §9.1 — wallet primitive, OAuth
// issuance, channel activation, verifier, and admin — so the attack surface is
// visible in the routing tree.
package httpapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	appmw "github.com/poolops/issuer/internal/httpapi/middleware"
	"github.com/poolops/issuer/internal/httpapi/respond"
)

// Deps carries the collaborators the handlers need. Fields are added as later
// plan items wire in real implementations.
type Deps struct {
	// reserved for stores, services, clock, etc.
}

// NewRouter builds the full route tree with cross-cutting middleware. Handlers
// not yet implemented respond 501; admin routes without a session respond 401.
func NewRouter(_ Deps) http.Handler {
	r := chi.NewRouter()
	// Global chain: request id → structured log → panic recovery.
	r.Use(chimw.RequestID)
	r.Use(appmw.RequestLogger)
	r.Use(chimw.Recoverer)

	// Per-IP rate limiters for the unauthenticated public/verifier planes.
	publicLimit := appmw.NewIPRateLimiter(20, 40).Middleware
	idem := appmw.NewIdempotency(10 * time.Minute).Middleware

	// Liveness — unauthenticated, always cheap.
	r.Get("/healthz", health)

	// ---- Wallet primitive plane (no auth; rate-limited nonce issuance) ----
	r.With(publicLimit).Post("/api/auth/challenge", notImplemented)

	// ---- Issuance (OAuth) plane ----
	r.Get("/connect", notImplemented)
	r.Post("/api/connect/authorize", notImplemented)
	r.With(idem).Post("/api/oauth/token", notImplemented) // idempotent create

	// ---- Channel activation plane (wallet signature, idempotent create) ----
	r.With(idem).Post("/api/activation/create", notImplemented)

	// ---- Verifier plane (public, read-only, rate-limited) ----
	r.Group(func(r chi.Router) {
		r.Use(publicLimit)
		r.Get("/.well-known/poolops/jwks.json", notImplemented)
		r.Post("/api/oauth/introspect", notImplemented)
		r.Post("/api/oauth/revoke", notImplemented)
	})

	// ---- Admin plane (owner-key session + RBAC + step-up) ----
	r.Route("/api/admin", func(r chi.Router) {
		r.Post("/auth/challenge", notImplemented)
		r.Post("/auth/verify", notImplemented)
		r.Group(func(r chi.Router) {
			r.Use(requireAdminSession) // stub gate until p8
			r.Get("/audit", notImplemented)
		})
	})

	return r
}

func health(w http.ResponseWriter, _ *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	respond.Error(w, http.StatusNotImplemented, "not_implemented", "endpoint not yet implemented")
}

// requireAdminSession is a placeholder gate replaced by the real session + RBAC
// middleware in p8-1. Until then it denies access so the admin plane is
// observably protected (TC-1).
func requireAdminSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "admin session required")
	})
}
