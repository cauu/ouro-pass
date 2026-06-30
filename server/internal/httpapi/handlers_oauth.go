package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"ouro-pass/server/internal/core/oauth"
	"ouro-pass/server/internal/httpapi/authpage"
	"ouro-pass/server/internal/httpapi/respond"
	"ouro-pass/server/internal/worker/telegram"
)

// connect serves the issuer-rendered Authorization Page (detailed §9.4, S0003).
// It validates the OAuth query parameters (failing fast for an unknown/invalid
// client) and returns the full HTML page; the embedded JS drives the CIP-30
// flow and posts the signed nonce to /api/connect/authorize.
//
//	GET /connect?client_id&redirect_uri&state&aud&response_type=code&code_challenge&scope?&device_pubkey?
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
	if err := authpage.RenderConnect(w, authpage.ConnectData{
		ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), State: q.Get("state"),
		Aud: q.Get("aud"), Scope: q.Get("scope"), CodeChallenge: q.Get("code_challenge"),
		DevicePubkey: q.Get("device_pubkey"),
	}); err != nil {
		serverError(w, r, err)
	}
}

// bind serves the issuer-rendered channel binding page (detailed §9.5, S0003):
// connect wallet → activation → Telegram deep link. Public; the server enforces
// eligibility when the activation is created.
//
//	GET /bind?channel_type=telegram[&channel_id=<id>]
//
// With no channel_id the page is a channel directory (S0018): it fetches
// /api/channels and lets the holder pick one. channel_id (S0016), when present,
// binds the page to one instance so the deep link uses that instance's bot; it is
// validated here (active telegram) so a stale link fails fast, and the instance
// name + bot username are passed through for the page to display.
func (h *apiHandlers) bind(w http.ResponseWriter, r *http.Request) {
	if h.d.OAuth == nil {
		notImplemented(w, r)
		return
	}
	ct := r.URL.Query().Get("channel_type")
	if ct == "" {
		ct = "telegram"
	}
	data := authpage.BindData{ChannelType: ct}
	if cid := r.URL.Query().Get("channel_id"); cid != "" {
		inst, err := h.d.Store.Channels().Get(r.Context(), cid)
		if err != nil || inst.Status != "active" || inst.ChannelType != "telegram" {
			respond.Error(w, http.StatusBadRequest, "invalid_request", "unknown or inactive channel instance")
			return
		}
		data.ChannelID = cid
		data.ChannelName = inst.Name
		data.BotUsername = telegram.DecodeUsername(inst.Config)
	}
	if err := authpage.RenderBind(w, data); err != nil {
		serverError(w, r, err)
	}
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
		CoseKey       string   `json:"cose_key"`
		Signature     string   `json:"signature"`
		CodeChallenge string   `json:"code_challenge"`
		DevicePubkey  string   `json:"device_pubkey"`
	}
	// The issuer-served Authorization Page submits a hidden form (so the browser
	// follows the 302 to redirect_uri natively); programmatic clients post JSON.
	if ct := r.Header.Get("Content-Type"); strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed form body")
			return
		}
		req.ClientID = r.PostForm.Get("client_id")
		req.RedirectURI = r.PostForm.Get("redirect_uri")
		req.State = r.PostForm.Get("state")
		req.Aud = r.PostForm.Get("aud")
		req.Scope = strings.Fields(r.PostForm.Get("scope"))
		req.Nonce = r.PostForm.Get("nonce")
		req.CoseKey = r.PostForm.Get("cose_key")
		req.Signature = r.PostForm.Get("signature")
		req.CodeChallenge = r.PostForm.Get("code_challenge")
		req.DevicePubkey = r.PostForm.Get("device_pubkey")
	} else if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}

	code, err := h.d.OAuth.Authorize(r.Context(), oauth.AuthorizeRequest{
		ClientID: req.ClientID, RedirectURI: req.RedirectURI, State: req.State, Aud: req.Aud,
		Scope: req.Scope, Nonce: req.Nonce, CoseKey: req.CoseKey, Signature: req.Signature,
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

// oauthToken exchanges an authorization code or refresh token for tokens
// (detailed §9.4). Accepts JSON or form-encoded bodies.
//
//	POST /api/oauth/token
func (h *apiHandlers) oauthToken(w http.ResponseWriter, r *http.Request) {
	if h.d.OAuth == nil {
		notImplemented(w, r)
		return
	}
	req, err := parseTokenRequest(r)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	resp, err := h.d.OAuth.Token(r.Context(), req)
	if err != nil {
		status := http.StatusBadRequest
		code := oauthTokenErrCode(err)
		if code == "invalid_client" {
			status = http.StatusUnauthorized
		} else if code == "not_eligible" {
			status = http.StatusForbidden
		}
		// Use a fixed description per code; never reflect err.Error() (which could
		// carry a wrapped internal/DB detail for an unmapped error) (p12-10).
		respond.Error(w, status, code, oauthErrDescription(code))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"access_token":  resp.AccessToken,
		"token_type":    resp.TokenType,
		"refresh_token": resp.RefreshToken,
		"expires_at":    resp.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		"membership":    map[string]string{"status": resp.Status, "tier": resp.Tier},
	})
}

func parseTokenRequest(r *http.Request) (oauth.TokenRequest, error) {
	var req oauth.TokenRequest
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var body struct {
			GrantType    string `json:"grant_type"`
			Code         string `json:"code"`
			ClientID     string `json:"client_id"`
			ClientSecret string `json:"client_secret"`
			CodeVerifier string `json:"code_verifier"`
			RedirectURI  string `json:"redirect_uri"`
			RefreshToken string `json:"refresh_token"`
			DevicePubkey string `json:"device_pubkey"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return req, err
		}
		req = oauth.TokenRequest(body)
		return req, nil
	}
	if err := r.ParseForm(); err != nil {
		return req, err
	}
	return oauth.TokenRequest{
		GrantType: r.PostFormValue("grant_type"), Code: r.PostFormValue("code"),
		ClientID: r.PostFormValue("client_id"), ClientSecret: r.PostFormValue("client_secret"),
		CodeVerifier: r.PostFormValue("code_verifier"), RedirectURI: r.PostFormValue("redirect_uri"),
		RefreshToken: r.PostFormValue("refresh_token"), DevicePubkey: r.PostFormValue("device_pubkey"),
	}, nil
}

func oauthTokenErrCode(err error) string {
	switch {
	case errors.Is(err, oauth.ErrInvalidGrant):
		return "invalid_grant"
	case errors.Is(err, oauth.ErrUnsupportedGrant):
		return "unsupported_grant_type"
	case errors.Is(err, oauth.ErrInvalidClientCreds), errors.Is(err, oauth.ErrInvalidClient):
		return "invalid_client"
	case errors.Is(err, oauth.ErrNotEligible):
		return "not_eligible"
	default:
		return "invalid_request"
	}
}

// oauthErrDescription maps an OAuth error code to a fixed, non-sensitive
// human description, so handlers never reflect raw err.Error() to clients (p12-10).
func oauthErrDescription(code string) string {
	switch code {
	case "invalid_grant":
		return "the authorization code or refresh token is invalid, expired, or already used"
	case "invalid_client":
		return "client authentication failed"
	case "not_eligible":
		return "the stake credential is not eligible for membership"
	case "unsupported_grant_type":
		return "unsupported grant_type"
	case "access_denied":
		return "wallet authorization failed"
	default:
		return "invalid request"
	}
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
	code := oauthErrCode(err)
	respond.Error(w, status, code, oauthErrDescription(code))
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
