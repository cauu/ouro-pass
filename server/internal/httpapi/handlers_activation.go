package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"ouro-pass/server/internal/core/oauth"
	"ouro-pass/server/internal/httpapi/respond"
	"ouro-pass/server/internal/worker/telegram"
)

// activationCreate issues a one-time channel activation code + deep link
// (detailed §9.5). The bot username for deep links comes from Deps.
//
//	POST /api/activation/create  {channel_type, nonce, cose_key, signature}
func (h *apiHandlers) activationCreate(w http.ResponseWriter, r *http.Request) {
	if h.d.OAuth == nil {
		notImplemented(w, r)
		return
	}
	var req struct {
		ChannelType string `json:"channel_type"`
		ChannelID   string `json:"channel_id"`
		Nonce       string `json:"nonce"`
		CoseKey     string `json:"cose_key"`
		Signature   string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	// Resolve the deep-link bot username: the selected instance's own username
	// (S0005 p2-2), falling back to the deployment-wide default bot.
	botUsername := h.d.TelegramBot
	if req.ChannelID != "" {
		inst, err := h.d.Store.Channels().Get(r.Context(), req.ChannelID)
		if err != nil || inst.Status != "active" || inst.ChannelType != "telegram" {
			respond.Error(w, http.StatusBadRequest, "invalid_request", "unknown or inactive channel instance")
			return
		}
		if u := telegram.DecodeUsername(inst.Config); u != "" {
			botUsername = u
		}
	}
	res, err := h.d.OAuth.CreateActivation(r.Context(), req.ChannelType, req.ChannelID, req.Nonce, req.CoseKey, req.Signature, botUsername)
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
