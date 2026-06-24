package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/httpapi/respond"
)

const sessionCookie = "ouropass_admin_session"

type ctxKey int

const adminUserKey ctxKey = iota

// adminChallenge issues an owner-key login nonce (detailed §9.8).
//
//	POST /api/admin/auth/challenge  {owner_stake_address} → {nonce}
func (h *apiHandlers) adminChallenge(w http.ResponseWriter, r *http.Request) {
	if h.d.Admin == nil {
		notImplemented(w, r)
		return
	}
	var req struct {
		OwnerStakeAddress string `json:"owner_stake_address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OwnerStakeAddress == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "owner_stake_address required")
		return
	}
	nonce, exp, err := h.d.Admin.Challenge(r.Context(), req.OwnerStakeAddress)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "could not issue challenge")
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"nonce": nonce, "expires_at": exp.UTC().Format("2006-01-02T15:04:05Z07:00")})
}

// adminVerify validates the signed nonce and issues an httpOnly session cookie.
//
//	POST /api/admin/auth/verify  {nonce, cose_key, signature} → set-cookie
func (h *apiHandlers) adminVerify(w http.ResponseWriter, r *http.Request) {
	if h.d.Admin == nil {
		notImplemented(w, r)
		return
	}
	var req struct {
		Nonce     string `json:"nonce"`
		CoseKey   string `json:"cose_key"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	token, role, err := h.d.Admin.Verify(r.Context(), req.CoseKey, req.Nonce, req.Signature, h.clientIP(r))
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: h.d.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	respond.JSON(w, http.StatusOK, map[string]string{"role": string(role)})
}

// adminLogout deletes the current session.
func (h *apiHandlers) adminLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && h.d.Admin != nil {
		_ = h.d.Admin.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	respond.JSON(w, http.StatusOK, map[string]bool{"logged_out": true})
}

// adminMe returns the current admin (any authenticated role).
func (h *apiHandlers) adminMe(w http.ResponseWriter, r *http.Request) {
	u := adminFromCtx(r.Context())
	respond.JSON(w, http.StatusOK, map[string]string{"admin_id": u.AdminID, "role": string(u.Role)})
}

// requireSession authenticates the admin session cookie and stores the user in
// the request context (401 otherwise).
func (h *apiHandlers) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.d.Admin == nil {
			notImplemented(w, r)
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			respond.Error(w, http.StatusUnauthorized, "unauthorized", "admin session required")
			return
		}
		user, err := h.d.Admin.Authenticate(r.Context(), c.Value)
		if err != nil {
			respond.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired session")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminUserKey, user)))
	})
}

// requireRole enforces a minimum RBAC role on an already-authenticated request.
func (h *apiHandlers) requireRole(min domain.AdminRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := adminFromCtx(r.Context())
			if u == nil || !admin.AtLeast(u.Role, min) {
				respond.Error(w, http.StatusForbidden, "forbidden", "insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func adminFromCtx(ctx context.Context) *domain.AdminUser {
	u, _ := ctx.Value(adminUserKey).(*domain.AdminUser)
	return u
}

func writeAdminErr(w http.ResponseWriter, err error) {
	switch err {
	case admin.ErrUnauthorized:
		respond.Error(w, http.StatusUnauthorized, "unauthorized", "signature verification failed")
	case admin.ErrForbidden:
		respond.Error(w, http.StatusForbidden, "forbidden", "key is not an authorized admin")
	default:
		// Never reflect an unmapped error's text (could wrap an internal detail).
		respond.Error(w, http.StatusBadRequest, "invalid_request", "invalid request")
	}
}

// clientIP resolves the caller's IP for audit/throttling. X-Forwarded-For is
// trusted ONLY when the deployment declares a trusted proxy (D15); otherwise the
// header is ignored (it is attacker-controlled with no proxy in front) and the
// transport RemoteAddr is used. Prevents audit-log IP forgery / throttle evasion
// via a spoofed header (p12-6).
func (h *apiHandlers) clientIP(r *http.Request) string {
	if h.d.TrustedProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip // rightmost hop = address the trusted proxy actually saw
			}
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
