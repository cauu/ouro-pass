package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
)

func TestOAuthClientRepo_RoundTrip(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now()

	c := domain.OAuthClient{
		ClientID: "c1", Name: "Ouro App", ClientType: domain.ClientConfidential,
		ClientSecretHash: ptr("hash"),
		RedirectURIs:     []string{"https://app/cb"}, AllowedAudiences: []string{"app:ouro"},
		Status: "active", CreatedAt: now,
	}
	if err := st.OAuthClients().Upsert(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := st.OAuthClients().Get(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientType != domain.ClientConfidential || len(got.AllowedAudiences) != 1 {
		t.Fatalf("mismatch: %+v", got)
	}

	// Public client (no secret).
	pub := domain.OAuthClient{
		ClientID: "spa", Name: "SPA", ClientType: domain.ClientPublic,
		RedirectURIs: []string{"https://spa/cb"}, AllowedAudiences: []string{"app:ouro"},
		Status: "active", CreatedAt: now,
	}
	st.OAuthClients().Upsert(ctx, pub)
	got, _ = st.OAuthClients().Get(ctx, "spa")
	if got.ClientType != domain.ClientPublic || got.ClientSecretHash != nil {
		t.Fatalf("public client: type=%v secret=%v", got.ClientType, got.ClientSecretHash)
	}
}

func TestChannelConfigRepo(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now()
	c := domain.ChannelConfig{
		ChannelID: "ch1", PoolID: "pool1", ChannelType: "telegram",
		Config: json.RawMessage(`{"bot_token_enc":"<enc>"}`), Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := st.Channels().Upsert(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := st.Channels().GetByType(ctx, "telegram")
	if err != nil || got.ChannelID != "ch1" || string(got.Config) != `{"bot_token_enc":"<enc>"}` {
		t.Fatalf("get: %v %+v", err, got)
	}
	if _, err := st.Channels().GetByType(ctx, "discord"); err != domain.ErrNotFound {
		t.Errorf("missing channel: %v", err)
	}
}

func TestSubscriptionSession_UpsertUniqueKey(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now().Truncate(time.Millisecond)

	mk := func(id, tier string) domain.SubscriptionSession {
		return domain.SubscriptionSession{
			SessionID: id, PoolID: "pool1", StakeCredentialHash: "h1", ChannelType: "telegram",
			ChannelUserID: "tg-42", Status: domain.SubActive, Tier: tier, Topics: []string{"news"},
			Entitlements: []string{"read"}, CreatedAt: now, LastVerifiedAt: now, ExpiresAt: now.Add(time.Hour),
		}
	}
	if err := st.Subscriptions().Upsert(ctx, mk("s1", "gold")); err != nil {
		t.Fatal(err)
	}
	// Same channel-user key upserts in place (tier change), not a duplicate.
	if err := st.Subscriptions().Upsert(ctx, mk("s1", "silver")); err != nil {
		t.Fatal(err)
	}
	got, err := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "tg-42")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != "silver" || got.Topics[0] != "news" {
		t.Fatalf("after upsert: %+v", got)
	}

	if err := st.Subscriptions().SetStatus(ctx, got.SessionID, domain.SubCancelled); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "tg-42")
	if got.Status != domain.SubCancelled {
		t.Errorf("status = %s, want cancelled", got.Status)
	}
}
