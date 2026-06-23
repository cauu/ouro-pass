package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/poolops/issuer/internal/core/oauth"
	"github.com/poolops/issuer/internal/httpapi/respond"
)

// connect serves the Authorization Page contract (detailed §9.4). The rich page
// lives in web/; this backend validates the OAuth query parameters and returns
// a minimal placeholder so an unconfigured/invalid client fails fast.
//
//	GET /connect?client_id&redirect_uri&state&aud&response_type=code&scope?&code_challenge?
func (h *apiHandlers) connect(w http.ResponseWriter, r *http.Request) {
	if h.d.OAuth == nil {
		notImplemented(w, r)
		return
	}
	q := r.URL.Query()
	if q.Get("response_type") != "code" {
		respond.Error(w, http.StatusBadRequest, "unsupported_response_type", "response_type must be code")
		return
	}
	if _, err := h.d.OAuth.ValidateClient(r.Context(), q.Get("client_id"), q.Get("redirect_uri"), q.Get("aud")); err != nil {
		writeOAuthErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `<!doctype html><html><head><title>PoolOps Authorization</title></head>
<body><h1>Connect your wallet</h1>
<p>client_id: %s</p><p>aud: %s</p>
<p>This placeholder is replaced by the web/ Authorization Page; it posts the
signed nonce to /api/connect/authorize.</p></body></html>`,
		htmlEscape(q.Get("client_id")), htmlEscape(q.Get("aud")))
}

// connectAuthorize handles the signed authorization submission (detailed §9.4).
//
//	POST /api/connect/authorize → 302 redirect_uri?code&state (or ?error=not_eligible)
func (h *apiHandlers) connectAuthorize(w http.ResponseWriter, r *http.Request) {
	if h.d.OAuth == nil {
		notImplemented(w, r)
		return
	}
	var req struct {
		ClientID      string   `json:"client_id"`
		RedirectURI   string   `json:"redirect_uri"`
		State         string   `json:"state"`
		Aud           string   `json:"aud"`
		Scope         []string `json:"scope"`
		Nonce         string   `json:"nonce"`
		StakeVkey     string   `json:"stake_vkey"`
		Signature     string   `json:"signature"`
		CodeChallenge string   `json:"code_challenge"`
		DevicePubkey  string   `json:"device_pubkey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}

	code, err := h.d.OAuth.Authorize(r.Context(), oauth.AuthorizeRequest{
		ClientID: req.ClientID, RedirectURI: req.RedirectURI, State: req.State, Aud: req.Aud,
		Scope: req.Scope, Nonce: req.Nonce, StakeVkey: req.StakeVkey, Signature: req.Signature,
		CodeChallenge: req.CodeChallenge, DevicePubkey: req.DevicePubkey,
	})
	if err != nil {
		// Eligibility/denial redirect back to the client with an error param;
		// malformed client/request fail directly.
		switch {
		case errors.Is(err, oauth.ErrNotEligible), errors.Is(err, oauth.ErrAccessDenied):
			redirectWithParams(w, r, req.RedirectURI, url.Values{"error": {oauthErrCode(err)}, "state": {req.State}})
		default:
			writeOAuthErr(w, err)
		}
		return
	}
	redirectWithParams(w, r, req.RedirectURI, url.Values{"code": {code}, "state": {req.State}})
}

func redirectWithParams(w http.ResponseWriter, r *http.Request, redirectURI string, params url.Values) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "bad redirect_uri")
		return
	}
	q := u.Query()
	for k, vs := range params {
		for _, v := range vs {
			if v != "" {
				q.Set(k, v)
			}
		}
	}
	u.RawQuery = q.Encode()
	w.Header().Set("Location", u.String())
	w.WriteHeader(http.StatusFound)
}

func writeOAuthErr(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, oauth.ErrInvalidClient) {
		status = http.StatusUnauthorized
	}
	respond.Error(w, status, oauthErrCode(err), err.Error())
}

func oauthErrCode(err error) string {
	switch {
	case errors.Is(err, oauth.ErrInvalidClient):
		return "invalid_client"
	case errors.Is(err, oauth.ErrNotEligible):
		return "not_eligible"
	case errors.Is(err, oauth.ErrAccessDenied):
		return "access_denied"
	default:
		return "invalid_request"
	}
}

func htmlEscape(s string) string {
	var out []rune
	for _, r := range s {
		switch r {
		case '<', '>', '&', '"', '\'':
			continue // drop markup-significant chars from echoed params
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
