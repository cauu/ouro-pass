// Package httpapi assembles the chi router. The route groups map 1:1 to the
// authentication planes in the detailed design §9.1 — wallet primitive, OAuth
// issuance, channel activation, verifier, and admin — so the attack surface is
// visible in the routing tree.
package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/oauth"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/httpapi/adminui"
	"ouro-pass/server/internal/httpapi/authpage"
	appmw "ouro-pass/server/internal/httpapi/middleware"
	"ouro-pass/server/internal/httpapi/respond"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
)

// Deps carries the collaborators the handlers need; nil services degrade their
// routes to 501 so the server still boots during incremental wiring.
type Deps struct {
	Wallet        *walletauth.Service
	Keys          *keys.Service
	OAuth         *oauth.Server
	Admin         *admin.Service
	Store         *store.Store // admin resource handlers use repos directly
	Chain         chain.Source // optional admin delegator roster (S0004 §2.7)
	PoolID        string
	TelegramBot   string // bot username for activation deep links
	Network       string // "mainnet"|"testnet"; when set, the auth page enforces a wallet network guard
	TrustedProxy  bool   // trust X-Forwarded-For for client IP (only behind a known proxy, D15)
	SecureCookies bool   // set Secure on admin session cookies (off for local HTTP, D17)
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
	// Rate-limited: these are unauthenticated and do expensive work (COSE verify,
	// external chain queries), so brute-force / amplification is bounded (p12-9).
	r.With(publicLimit).Get("/connect", h.connect)
	r.With(publicLimit).Post("/api/connect/authorize", h.connectAuthorize)
	r.With(publicLimit, idem).Post("/api/oauth/token", h.oauthToken)

	// ---- Channel activation plane ----
	r.With(publicLimit).Get("/bind", h.bind)
	r.With(publicLimit, idem).Post("/api/activation/create", h.activationCreate)

	// Issuer-served Authorization/binding page asset (same-origin script).
	r.Get("/assets/ouropass-auth.js", authpage.Asset().ServeHTTP)

	// ---- Verifier plane (public, read-only, rate-limited) ----
	r.Group(func(r chi.Router) {
		r.Use(publicLimit)
		r.Get("/.well-known/ouropass/jwks.json", h.jwks)
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
			r.Post("/auth/step-up/challenge", h.adminStepUpChallenge)
			r.With(h.requireRole(domain.RoleViewer)).Get("/me", h.adminMe)
			h.mountAdminResources(r) // p8-2 resource endpoints
		})
	})

	// ---- Admin SPA (embedded static, served under /admin; S0002) ----
	r.Handle("/admin", http.RedirectHandler("/admin/", http.StatusMovedPermanently))
	r.Handle("/admin/*", http.StripPrefix("/admin", adminui.Handler()))

	return r
}

func health(w http.ResponseWriter, _ *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	respond.Error(w, http.StatusNotImplemented, "not_implemented", "endpoint not yet implemented")
}

// serverError logs the underlying error server-side (with the request id) and
// returns a generic 500 to the client — internal/DB error details are never
// disclosed to callers (F1).
func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "err", err, "method", r.Method, "path", r.URL.Path,
		"req_id", chimw.GetReqID(r.Context()))
	respond.Error(w, http.StatusInternalServerError, "server_error", "internal error")
}
