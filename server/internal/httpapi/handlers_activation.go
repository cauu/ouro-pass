package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"ouro-pass/server/internal/core/oauth"
	"ouro-pass/server/internal/httpapi/respond"
)

// activationCreate issues a one-time channel activation code + deep link
// (detailed §9.5). The bot username for deep links comes from Deps.
//
//	POST /api/activation/create  {channel_type, nonce, stake_vkey, signature}
func (h *apiHandlers) activationCreate(w http.ResponseWriter, r *http.Request) {
	if h.d.OAuth == nil {
		notImplemented(w, r)
		return
	}
	var req struct {
		ChannelType string `json:"channel_type"`
		Nonce       string `json:"nonce"`
		StakeVkey   string `json:"stake_vkey"`
		Signature   string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	res, err := h.d.OAuth.CreateActivation(r.Context(), req.ChannelType, req.Nonce, req.StakeVkey, req.Signature, h.d.TelegramBot)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, oauth.ErrNotEligible) {
			status = http.StatusForbidden
		}
		respond.Error(w, status, oauthErrCode(err), err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"activation_code": res.ActivationCode,
		"deep_link":       res.DeepLink,
		"expires_at":      res.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}
