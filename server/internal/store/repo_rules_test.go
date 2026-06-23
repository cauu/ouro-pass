package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
)

func TestMembershipRuleRepo_ListActiveOrdered(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now()

	mk := func(id, tier string, prio int, status domain.RuleStatus) domain.MembershipRule {
		return domain.MembershipRule{
			RuleID: id, Name: id, RuleConfig: json.RawMessage(`{"min_active_stake_lovelace":"1000000"}`),
			Tier: tier, Entitlements: []string{"news"}, Priority: prio, Status: status,
			CreatedAt: now, UpdatedAt: now,
		}
	}
	for _, r := range []domain.MembershipRule{
		mk("gold", "gold", 10, domain.RuleActive),
		mk("silver", "silver", 5, domain.RuleActive),
		mk("old", "bronze", 99, domain.RuleDisabled), // excluded
	} {
		if err := st.Rules().Upsert(ctx, r); err != nil {
			t.Fatalf("upsert %s: %v", r.RuleID, err)
		}
	}

	active, err := st.Rules().ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Fatalf("active rules = %d, want 2 (disabled excluded)", len(active))
	}
	// Deterministic ordering: priority desc → gold(10) before silver(5).
	if active[0].RuleID != "gold" || active[1].RuleID != "silver" {
		t.Fatalf("order = [%s,%s], want [gold,silver]", active[0].RuleID, active[1].RuleID)
	}
	if active[0].Entitlements[0] != "news" {
		t.Errorf("entitlements round-trip failed: %v", active[0].Entitlements)
	}

	// Upsert mutates in place.
	g := mk("gold", "gold", 1, domain.RuleActive)
	g.Name = "Gold v2"
	st.Rules().Upsert(ctx, g)
	active, _ = st.Rules().ListActive(ctx)
	// Now silver(5) outranks gold(1).
	if active[0].RuleID != "silver" {
		t.Errorf("after reprioritize, first = %s, want silver", active[0].RuleID)
	}
}

func TestBlacklistAndSnapshotCache(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now()

	has, _ := st.Blacklist().Has(ctx, "h1")
	if has {
		t.Fatal("h1 should not be blacklisted initially")
	}
	if err := st.Blacklist().Add(ctx, domain.Blacklist{StakeCredentialHash: "h1", Reason: ptr("abuse"), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if has, _ := st.Blacklist().Has(ctx, "h1"); !has {
		t.Fatal("h1 should be blacklisted")
	}

	snap := domain.StakeSnapshotCache{
		StakeCredentialHash: "h1", SnapshotEpoch: 480, DelegatedPoolID: ptr("pool1abc"),
		ActiveStakeLovelace: ptr("45000000000000000"), Source: "db_sync", FetchedAt: now,
	}
	if err := st.SnapshotCache().Upsert(ctx, snap); err != nil {
		t.Fatal(err)
	}
	got, err := st.SnapshotCache().Get(ctx, "h1")
	if err != nil {
		t.Fatal(err)
	}
	// Lovelace beyond 2^53 survives as exact decimal string (C4).
	if got.ActiveStakeLovelace == nil || *got.ActiveStakeLovelace != "45000000000000000" {
		t.Fatalf("active stake = %v, want exact big value", got.ActiveStakeLovelace)
	}
	if got.SnapshotEpoch != 480 {
		t.Errorf("epoch = %d", got.SnapshotEpoch)
	}
}
