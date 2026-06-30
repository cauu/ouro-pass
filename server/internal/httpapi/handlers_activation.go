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
	// Resolve the deep-link bot username from the targeted channel instance only
	// (S0017: there is no env-default fallback bot). channel_id is required and must
	// resolve to an active telegram instance that carries a public bot username, so
	// the deep link always points at a real, running bot rather than a stale default.
	if req.ChannelID == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "channel_id is required")
		return
	}
	inst, err := h.d.Store.Channels().Get(r.Context(), req.ChannelID)
	if err != nil || inst.Status != "active" || inst.ChannelType != "telegram" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "unknown or inactive channel instance")
		return
	}
	botUsername := telegram.DecodeUsername(inst.Config)
	if botUsername == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "channel instance has no bot username configured")
		return
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
