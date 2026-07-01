package store

import (
	"context"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
)

// TestSubscriptionRepo_GraceUntilRoundTrip covers S0019 p3-2 / TC-10: the nullable
// grace_until column (migration 0016) round-trips through Upsert→Get — NULL stays
// NULL, a set deadline is preserved, and a clearing Upsert writes NULL back. This
// guards the two-field lifecycle (grace_until == nil is the sole "not in grace"
// signal) against a dialect-specific NULL-handling regression.
func TestSubscriptionRepo_GraceUntilRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now().Truncate(time.Millisecond).UTC()

	base := domain.SubscriptionSession{
		SessionID: "s1", PoolID: "pool1", StakeCredentialHash: "sch1", ChannelID: "tg1",
		ChannelType: "telegram", ChannelUserID: "u1", Status: domain.SubActive, Tier: "gold",
		CreatedAt: now, LastVerifiedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour),
	}

	// 1. Insert with GraceUntil nil → stored as NULL, read back as nil.
	if err := st.Subscriptions().Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	got, err := st.Subscriptions().GetByInstanceUser(ctx, "tg1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.GraceUntil != nil {
		t.Fatalf("fresh session GraceUntil = %v, want nil", got.GraceUntil)
	}

	// 2. Set a deadline → preserved on read (to the millisecond).
	deadline := now.Add(5 * 24 * time.Hour)
	base.GraceUntil = &deadline
	if err := st.Subscriptions().Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Subscriptions().GetByInstanceUser(ctx, "tg1", "u1")
	if got.GraceUntil == nil || !got.GraceUntil.Equal(deadline) {
		t.Fatalf("GraceUntil = %v, want %v", got.GraceUntil, deadline)
	}

	// 3. Clear it (member recovered) → NULL again.
	base.GraceUntil = nil
	if err := st.Subscriptions().Upsert(ctx, base); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Subscriptions().GetByInstanceUser(ctx, "tg1", "u1")
	if got.GraceUntil != nil {
		t.Fatalf("cleared GraceUntil = %v, want nil", got.GraceUntil)
	}
}
