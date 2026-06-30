package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
)

// TestPublicChannels covers S0018 TC-1: GET /api/channels lists active telegram
// instances that have a bot username, exposing only public fields; inactive,
// non-telegram, and username-less instances are excluded, and no token is leaked.
func TestPublicChannels(t *testing.T) {
	deps, _, _, _ := oauthDeps(t)
	ctx := context.Background()

	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	// Listed: active telegram with a username.
	must(deps.Store.Channels().Upsert(ctx, domain.ChannelConfig{
		ChannelID: "tg-a", PoolID: deps.PoolID, ChannelType: "telegram", Name: "Main",
		Config: []byte(`{"bot_token_enc":"deadbeef","bot_username":"main_bot"}`), Status: "active", CreatedAt: time.Now(),
	}))
	// Excluded: disabled.
	must(deps.Store.Channels().Upsert(ctx, domain.ChannelConfig{
		ChannelID: "tg-off", PoolID: deps.PoolID, ChannelType: "telegram", Name: "Old",
		Config: []byte(`{"bot_username":"old_bot"}`), Status: "disabled", CreatedAt: time.Now(),
	}))
	// Excluded: active but no username (not bind-usable).
	must(deps.Store.Channels().Upsert(ctx, domain.ChannelConfig{
		ChannelID: "tg-nouser", PoolID: deps.PoolID, ChannelType: "telegram", Name: "NoUser",
		Config: []byte(`{"bot_token_enc":"deadbeef"}`), Status: "active", CreatedAt: time.Now(),
	}))

	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/channels")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/channels = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	var out struct {
		Channels []map[string]any `json:"channels"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}

	if len(out.Channels) != 1 {
		t.Fatalf("listed %d channels, want 1 (only active+username): %s", len(out.Channels), raw)
	}
	c := out.Channels[0]
	if c["channel_id"] != "tg-a" || c["name"] != "Main" || c["channel_type"] != "telegram" || c["bot_username"] != "main_bot" {
		t.Errorf("public fields = %v", c)
	}
	// No token material is ever exposed.
	for _, leaked := range []string{"bot_token_enc", "token", "config", "token_hint"} {
		if _, ok := c[leaked]; ok {
			t.Errorf("response leaks %q: %v", leaked, c)
		}
	}
}
