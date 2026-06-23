package httpapi

import (
	"encoding/json"
	"net/http"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/httpapi/respond"
)

// authChallenge issues a wallet-signing nonce (detailed §9.3).
//
//	POST /api/auth/challenge  {purpose:"issue|activation", stake_vkey} → {nonce, expires_at}
func (h *apiHandlers) authChallenge(w http.ResponseWriter, r *http.Request) {
	if h.d.Wallet == nil {
		notImplemented(w, r)
		return
	}
	var req struct {
		Purpose   string `json:"purpose"`
		StakeVkey string `json:"stake_vkey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	purpose, ok := memberNoncePurpose(req.Purpose)
	if !ok {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "purpose must be issue or activation")
		return
	}
	if req.StakeVkey == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "stake_vkey required")
		return
	}
	nonce, expiresAt, err := h.d.Wallet.Challenge(r.Context(), purpose, req.StakeVkey)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"nonce":      nonce,
		"expires_at": expiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// memberNoncePurpose maps the public purpose values to nonce purposes.
func memberNoncePurpose(p string) (domain.NoncePurpose, bool) {
	switch p {
	case "issue":
		return domain.NonceIssue, true
	case "activation":
		return domain.NonceActivation, true
	default:
		return "", false
	}
}
