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
	"github.com/poolops/issuer/internal/core/admin"
	"github.com/poolops/issuer/internal/core/keys"
	"github.com/poolops/issuer/internal/core/oauth"
	"github.com/poolops/issuer/internal/core/walletauth"
	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/store"
	appmw "github.com/poolops/issuer/internal/httpapi/middleware"
	"github.com/poolops/issuer/internal/httpapi/respond"
)

// Deps carries the collaborators the handlers need; nil services degrade their
// routes to 501 so the server still boots during incremental wiring.
type Deps struct {
	Wallet      *walletauth.Service
	Keys        *keys.Service
	OAuth       *oauth.Server
	Admin       *admin.Service
	Store       *store.Store // admin resource handlers use repos directly
	ServerSalt  []byte       // for deriving member `sub` in the admin roster
	PoolID      string
	TelegramBot string // bot username for activation deep links
}

type apiHandlers struct{ d Deps }

// NewRouter builds the full route tree with cross-cutting middleware.
func NewRouter(d Deps) http.Handler {
	h := &apiHandlers{d}

	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(appmw.RequestLogger)
	r.Use(chimw.Recoverer)

	publicLimit := appmw.NewIPRateLimiter(20, 40).Middleware
	idem := appmw.NewIdempotency(10 * time.Minute).Middleware

	r.Get("/healthz", health)

	// ---- Wallet primitive plane ----
	r.With(publicLimit).Post("/api/auth/challenge", h.authChallenge)

	// ---- Issuance (OAuth) plane ----
	r.Get("/connect", h.connect)
	r.Post("/api/connect/authorize", h.connectAuthorize)
	r.With(idem).Post("/api/oauth/token", h.oauthToken)

	// ---- Channel activation plane ----
	r.With(idem).Post("/api/activation/create", h.activationCreate)

	// ---- Verifier plane (public, read-only, rate-limited) ----
	r.Group(func(r chi.Router) {
		r.Use(publicLimit)
		r.Get("/.well-known/poolops/jwks.json", h.jwks)
		r.Post("/api/oauth/introspect", h.introspect)
		r.Post("/api/oauth/revoke", h.revoke)
	})

	// ---- Admin plane (owner-key session + RBAC + step-up) ----
	r.Route("/api/admin", func(r chi.Router) {
		r.Post("/auth/challenge", h.adminChallenge)
		r.Post("/auth/verify", h.adminVerify)
		r.Group(func(r chi.Router) {
			r.Use(h.requireSession)
			r.Post("/auth/logout", h.adminLogout)
			r.With(h.requireRole(domain.RoleViewer)).Get("/me", h.adminMe)
			h.mountAdminResources(r) // p8-2 resource endpoints
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
