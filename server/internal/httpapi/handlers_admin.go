package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/poolops/issuer/internal/core/admin"
	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/httpapi/respond"
)

const sessionCookie = "poolops_admin_session"

type ctxKey int

const adminUserKey ctxKey = iota

// adminChallenge issues an owner-key login nonce (detailed §9.8).
//
//	POST /api/admin/auth/challenge  {owner_vkey} → {nonce}
func (h *apiHandlers) adminChallenge(w http.ResponseWriter, r *http.Request) {
	if h.d.Admin == nil {
		notImplemented(w, r)
		return
	}
	var req struct {
		OwnerVkey string `json:"owner_vkey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OwnerVkey == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "owner_vkey required")
		return
	}
	nonce, exp, err := h.d.Admin.Challenge(r.Context(), req.OwnerVkey)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"nonce": nonce, "expires_at": exp.UTC().Format("2006-01-02T15:04:05Z07:00")})
}

// adminVerify validates the signed nonce and issues an httpOnly session cookie.
//
//	POST /api/admin/auth/verify  {nonce, owner_vkey, signature} → set-cookie
func (h *apiHandlers) adminVerify(w http.ResponseWriter, r *http.Request) {
	if h.d.Admin == nil {
		notImplemented(w, r)
		return
	}
	var req struct {
		Nonce     string `json:"nonce"`
		OwnerVkey string `json:"owner_vkey"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	token, role, err := h.d.Admin.Verify(r.Context(), req.OwnerVkey, req.Nonce, req.Signature, clientIPFromReq(r))
	if err != nil {
		writeAdminErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: true,
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

// mountAdminResources mounts the admin resource endpoints (p8-2). Defined here
// as a stub so the router compiles after p8-1.
func (h *apiHandlers) mountAdminResources(r chi.Router) {}

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
		respond.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
	}
}

func clientIPFromReq(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-For"); h != "" {
		return h
	}
	return r.RemoteAddr
}
