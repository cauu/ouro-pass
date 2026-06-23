package store

import (
	"context"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
)

// TestRevokeByStakeCredential_Cascade verifies bulk revocation by sch affects
// only the target credential's active rows (admin member revoke, D10/§9.8).
func TestRevokeByStakeCredential_Cascade(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now().Truncate(time.Millisecond)
	const victim, bystander = "sch-victim", "sch-other"

	// Two members each with a token, a grant, and a session.
	for _, sch := range []string{victim, bystander} {
		if err := st.IssuedTokens().Create(ctx, nil, domain.IssuedToken{
			JTI: "jti-" + sch, StakeCredentialHash: sch, Kind: domain.TokenAccess, Audience: "app",
			KID: "k1", Status: domain.TokenActive, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.RefreshGrants().Create(ctx, nil, domain.RefreshGrant{
			RefreshGrantID: "g-" + sch, StakeCredentialHash: sch, Audience: "app",
			ClientType: domain.ClientPublic, Status: domain.GrantActive, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.Subscriptions().Upsert(ctx, domain.SubscriptionSession{
			SessionID: "s-" + sch, PoolID: "pool1", StakeCredentialHash: sch, ChannelType: "telegram",
			ChannelUserID: "u-" + sch, Status: domain.SubActive, Tier: "gold",
			CreatedAt: now, LastVerifiedAt: now, ExpiresAt: now.Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Revoke the victim by sch.
	if n, err := st.IssuedTokens().RevokeByStakeCredential(ctx, victim, now); err != nil || n != 1 {
		t.Fatalf("revoke tokens: %v n=%d", err, n)
	}
	if n, err := st.RefreshGrants().RevokeByStakeCredential(ctx, victim); err != nil || n != 1 {
		t.Fatalf("revoke grants: %v n=%d", err, n)
	}
	if n, err := st.Subscriptions().CancelByStakeCredential(ctx, victim); err != nil || n != 1 {
		t.Fatalf("cancel subs: %v n=%d", err, n)
	}

	// Victim's rows are revoked/cancelled.
	vt, _ := st.IssuedTokens().Get(ctx, "jti-"+victim)
	vg, _ := st.RefreshGrants().Get(ctx, "g-"+victim)
	vs, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-"+victim)
	if vt.Status != domain.TokenRevoked || vg.Status != domain.GrantRevoked || vs.Status != domain.SubCancelled {
		t.Fatalf("victim not fully revoked: token=%s grant=%s sub=%s", vt.Status, vg.Status, vs.Status)
	}

	// Bystander untouched.
	bt, _ := st.IssuedTokens().Get(ctx, "jti-"+bystander)
	bg, _ := st.RefreshGrants().Get(ctx, "g-"+bystander)
	bs, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-"+bystander)
	if bt.Status != domain.TokenActive || bg.Status != domain.GrantActive || bs.Status != domain.SubActive {
		t.Fatalf("bystander affected: token=%s grant=%s sub=%s", bt.Status, bg.Status, bs.Status)
	}

	// Re-revoking the victim is a no-op (0 rows).
	if n, _ := st.IssuedTokens().RevokeByStakeCredential(ctx, victim, now); n != 0 {
		t.Errorf("idempotent token revoke affected %d rows, want 0", n)
	}
}
