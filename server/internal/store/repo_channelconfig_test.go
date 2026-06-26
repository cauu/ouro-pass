package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
)

func chanInst(id, pool, typ, name, status string) domain.ChannelConfig {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return domain.ChannelConfig{
		ChannelID: id, PoolID: pool, ChannelType: typ, Name: name,
		Config: []byte(`{}`), Status: status, CreatedAt: now, UpdatedAt: now,
	}
}

func TestChannelConfig_CRUD(t *testing.T) {
	ctx := context.Background()
	repo := migratedStore(t).Channels()

	// Create two instances of the same type under one pool (multi-instance).
	if err := repo.Create(ctx, chanInst("c1", "pool1", "telegram", "members", "active")); err != nil {
		t.Fatalf("create c1: %v", err)
	}
	if err := repo.Create(ctx, chanInst("c2", "pool1", "telegram", "announce", "active")); err != nil {
		t.Fatalf("create c2: %v", err)
	}

	// Duplicate name within (pool, type) is rejected.
	if err := repo.Create(ctx, chanInst("c3", "pool1", "telegram", "members", "active")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate name err = %v, want ErrConflict", err)
	}

	// Get by id.
	got, err := repo.Get(ctx, "c1")
	if err != nil || got.Name != "members" {
		t.Fatalf("get c1 = %+v, %v", got, err)
	}
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get missing err = %v, want ErrNotFound", err)
	}

	// List returns both, ordered by (type, name): announce before members.
	all, err := repo.List(ctx, "pool1")
	if err != nil || len(all) != 2 || all[0].Name != "announce" || all[1].Name != "members" {
		t.Fatalf("list = %+v, %v", all, err)
	}

	// Disable c2 → ListActive drops it.
	if err := repo.SetStatus(ctx, "c2", "disabled", time.Now()); err != nil {
		t.Fatalf("disable c2: %v", err)
	}
	active, err := repo.ListActive(ctx, "pool1", "telegram")
	if err != nil || len(active) != 1 || active[0].ChannelID != "c1" {
		t.Fatalf("active = %+v, %v", active, err)
	}

	// Delete c1.
	if err := repo.Delete(ctx, "c1"); err != nil {
		t.Fatalf("delete c1: %v", err)
	}
	if _, err := repo.Get(ctx, "c1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("c1 still present after delete: %v", err)
	}
}

func TestSubscription_InstanceAddressing(t *testing.T) {
	ctx := context.Background()
	subs := migratedStore(t).Subscriptions()
	now := time.Now().UTC()

	mk := func(id, channelID, user string) domain.SubscriptionSession {
		return domain.SubscriptionSession{
			SessionID: id, PoolID: "pool1", StakeCredentialHash: "sch-" + user,
			ChannelID: channelID, ChannelType: "telegram", ChannelUserID: user,
			Status: domain.SubActive, Tier: "gold",
			CreatedAt: now, LastVerifiedAt: now, ExpiresAt: now.Add(time.Hour),
		}
	}
	// Same user subscribes on two different instances → two independent rows.
	for _, s := range []domain.SubscriptionSession{
		mk("s1", "c1", "u1"), mk("s2", "c2", "u1"), mk("s3", "c1", "u2"),
	} {
		if err := subs.Upsert(ctx, s); err != nil {
			t.Fatalf("upsert %s: %v", s.SessionID, err)
		}
	}

	// GetByInstanceUser disambiguates by instance.
	g1, err := subs.GetByInstanceUser(ctx, "c1", "u1")
	if err != nil || g1.SessionID != "s1" {
		t.Fatalf("get (c1,u1) = %+v, %v", g1, err)
	}
	g2, err := subs.GetByInstanceUser(ctx, "c2", "u1")
	if err != nil || g2.SessionID != "s2" {
		t.Fatalf("get (c2,u1) = %+v, %v", g2, err)
	}

	// ListActiveByInstance scopes to the instance.
	c1subs, err := subs.ListActiveByInstance(ctx, "c1")
	if err != nil || len(c1subs) != 2 {
		t.Fatalf("c1 active = %d (%v), want 2", len(c1subs), err)
	}

	// CancelByChannelID cascades only that instance's sessions.
	n, err := subs.CancelByChannelID(ctx, "c1")
	if err != nil || n != 2 {
		t.Fatalf("cancel c1 = %d (%v), want 2", n, err)
	}
	if left, _ := subs.ListActiveByInstance(ctx, "c1"); len(left) != 0 {
		t.Fatalf("c1 still has %d active after cancel", len(left))
	}
	if other, _ := subs.ListActiveByInstance(ctx, "c2"); len(other) != 1 {
		t.Fatalf("c2 active = %d, want 1 (cascade must not cross instances)", len(other))
	}
}
