package httpapi

import (
	"net/http"

	"ouro-pass/server/internal/httpapi/respond"
	"ouro-pass/server/internal/worker/telegram"
)

// publicChannel is the public, bind-safe view of a channel instance: only fields a
// holder needs to pick a channel (S0018). The encrypted token / token hint are
// never exposed here.
type publicChannel struct {
	ChannelID   string `json:"channel_id"`
	Name        string `json:"name"`
	ChannelType string `json:"channel_type"`
	BotUsername string `json:"bot_username"`
}

// publicChannels lists the channels a holder can bind to (S0018): the active
// telegram instances that carry a public bot username, so the bind page can show a
// directory and link to /bind?channel_id=<id>. Public + rate-limited; only public
// fields are returned.
//
//	GET /api/channels  ->  {channels: [{channel_id, name, channel_type, bot_username}]}
func (h *apiHandlers) publicChannels(w http.ResponseWriter, r *http.Request) {
	out := []publicChannel{}
	if h.d.Store != nil {
		insts, err := h.d.Store.Channels().ListActive(r.Context(), h.d.PoolID, "telegram")
		if err != nil {
			serverError(w, r, err)
			return
		}
		for _, inst := range insts {
			username := telegram.DecodeUsername(inst.Config)
			if username == "" {
				continue // not bind-usable without a deep-link bot username
			}
			out = append(out, publicChannel{
				ChannelID:   inst.ChannelID,
				Name:        inst.Name,
				ChannelType: inst.ChannelType,
				BotUsername: username,
			})
		}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"channels": out})
}
