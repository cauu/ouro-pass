package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"ouro-pass/server/internal/httpapi/respond"
)

// introspect reports a token's status (RFC 7662, detailed §9.6).
//
//	POST /api/oauth/introspect  {token} | {jti}
func (h *apiHandlers) introspect(w http.ResponseWriter, r *http.Request) {
	if h.d.OAuth == nil {
		notImplemented(w, r)
		return
	}
	b := parseTokenBody(r)
	// Only a full, signature-verified token is accepted — a caller-supplied bare
	// jti is ignored so this unauthenticated endpoint can't be used as a
	// token-status oracle by jti enumeration (RFC 7662 token-scanning, D16/p12-9).
	res, err := h.d.OAuth.Introspect(r.Context(), b.token, "")
	if err != nil {
		serverError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, res)
}

// revoke revokes an access or refresh token (RFC 7009). Always 200 per the RFC.
//
//	POST /api/oauth/revoke  {token, token_type_hint}
func (h *apiHandlers) revoke(w http.ResponseWriter, r *http.Request) {
	if h.d.OAuth == nil {
		notImplemented(w, r)
		return
	}
	b := parseTokenBody(r)
	if err := h.d.OAuth.Revoke(r.Context(), b.token, b.hint); err != nil {
		serverError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

type tokenBody struct {
	token string
	jti   string
	hint  string
}

// parseTokenBody reads token/jti/token_type_hint from a JSON or form body once.
func parseTokenBody(r *http.Request) tokenBody {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Token         string `json:"token"`
			JTI           string `json:"jti"`
			TokenTypeHint string `json:"token_type_hint"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		return tokenBody{token: body.Token, jti: body.JTI, hint: body.TokenTypeHint}
	}
	_ = r.ParseForm()
	return tokenBody{
		token: r.PostFormValue("token"),
		jti:   r.PostFormValue("jti"),
		hint:  r.PostFormValue("token_type_hint"),
	}
}
