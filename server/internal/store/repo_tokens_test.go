package store

import (
	"context"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
)

func TestIssuedToken_CreateGetRevoke(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now().Truncate(time.Millisecond)

	tok := domain.IssuedToken{
		JTI: "jti-1", StakeCredentialHash: "h1", Kind: domain.TokenAccess, Audience: "app:ouro",
		KID: "k1", ClientID: ptr("c1"), Status: domain.TokenActive, IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := st.IssuedTokens().Create(ctx, nil, tok); err != nil {
		t.Fatal(err)
	}
	got, err := st.IssuedTokens().Get(ctx, "jti-1")
	if err != nil || got.Status != domain.TokenActive || got.ClientID == nil || *got.ClientID != "c1" {
		t.Fatalf("get: %v %+v", err, got)
	}
	if err := st.IssuedTokens().Revoke(ctx, "jti-1", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = st.IssuedTokens().Get(ctx, "jti-1")
	if got.Status != domain.TokenRevoked || got.RevokedAt == nil {
		t.Fatalf("after revoke: %s %v", got.Status, got.RevokedAt)
	}
}

func TestRefreshGrant_RotationChainRevoke(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now()

	// Build a rotation chain g1 -> g2 -> g3.
	chain := []struct{ id, from string }{{"g1", ""}, {"g2", "g1"}, {"g3", "g2"}}
	for _, c := range chain {
		g := domain.RefreshGrant{
			RefreshGrantID: c.id, StakeCredentialHash: "h1", Audience: "app", ClientType: domain.ClientPublic,
			Status: domain.GrantActive, CreatedAt: now,
		}
		if c.from != "" {
			g.RotatedFrom = ptr(c.from)
			g.Status = domain.GrantActive
		}
		if err := st.RefreshGrants().Create(ctx, nil, g); err != nil {
			t.Fatalf("create %s: %v", c.id, err)
		}
	}
	// Replaying g1 (a rotated ancestor) revokes the whole chain.
	if err := st.RefreshGrants().RevokeChain(ctx, "g1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"g1", "g2", "g3"} {
		g, _ := st.RefreshGrants().Get(ctx, id)
		if g.Status != domain.GrantRevoked {
			t.Errorf("%s status = %s, want revoked", id, g.Status)
		}
	}
}

func TestAuthNonce_ConsumeOnceWithGuards(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now()

	mk := func(nonce string, purpose domain.NoncePurpose, exp time.Time) {
		if err := st.AuthNonces().Create(ctx, domain.AuthNonce{
			Nonce: nonce, Purpose: purpose, ExpiresAt: exp, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	mk("n1", domain.NonceIssue, now.Add(time.Minute))
	if _, err := st.AuthNonces().Consume(ctx, "n1", domain.NonceIssue, now); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	// Replay → ErrConsumed.
	if _, err := st.AuthNonces().Consume(ctx, "n1", domain.NonceIssue, now); err != domain.ErrConsumed {
		t.Fatalf("replay: %v, want ErrConsumed", err)
	}
	// Missing → ErrNotFound.
	if _, err := st.AuthNonces().Consume(ctx, "nope", domain.NonceIssue, now); err != domain.ErrNotFound {
		t.Fatalf("missing: %v, want ErrNotFound", err)
	}
	// Wrong purpose → ErrPurpose.
	mk("n2", domain.NonceIssue, now.Add(time.Minute))
	if _, err := st.AuthNonces().Consume(ctx, "n2", domain.NonceAdminLogin, now); err != domain.ErrPurpose {
		t.Fatalf("purpose: %v, want ErrPurpose", err)
	}
	// Expired → ErrExpired.
	mk("n3", domain.NonceIssue, now.Add(-time.Minute))
	if _, err := st.AuthNonces().Consume(ctx, "n3", domain.NonceIssue, now); err != domain.ErrExpired {
		t.Fatalf("expired: %v, want ErrExpired", err)
	}
}

func TestAuthNonce_DeleteExpired(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now()

	mk := func(nonce string, exp time.Time) {
		if err := st.AuthNonces().Create(ctx, domain.AuthNonce{
			Nonce: nonce, Purpose: domain.NonceIssue, ExpiresAt: exp, CreatedAt: now.Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("expired-1", now.Add(-time.Minute)) // past
	mk("expired-2", now.Add(-time.Second)) // past
	mk("valid-1", now.Add(time.Minute))    // future

	n, err := st.AuthNonces().DeleteExpired(ctx, now)
	if err != nil || n != 2 {
		t.Fatalf("DeleteExpired removed %d (err %v), want 2", n, err)
	}
	// Expired gone, valid one still consumable.
	if _, err := st.AuthNonces().Consume(ctx, "expired-1", domain.NonceIssue, now); err != domain.ErrNotFound {
		t.Errorf("expired-1 consume = %v, want ErrNotFound", err)
	}
	if _, err := st.AuthNonces().Consume(ctx, "valid-1", domain.NonceIssue, now); err != nil {
		t.Errorf("valid-1 should still be consumable: %v", err)
	}
	// Idempotent: nothing left to delete.
	if n, _ := st.AuthNonces().DeleteExpired(ctx, now); n != 0 {
		t.Errorf("second DeleteExpired removed %d, want 0", n)
	}
}

func TestAuthCodeAndActivation_ConsumeOnce(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now()

	if err := st.AuthCodes().Create(ctx, domain.AuthorizationCode{
		Code: "code1", ClientID: "c1", StakeCredentialHash: "h1", Aud: "app",
		Scope: []string{"read"}, RedirectURI: "https://cb", CodeChallenge: ptr("chal"),
		ExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	ac, err := st.AuthCodes().Consume(ctx, "code1", now)
	if err != nil || ac.ClientID != "c1" || len(ac.Scope) != 1 || ac.CodeChallenge == nil {
		t.Fatalf("consume authcode: %v %+v", err, ac)
	}
	if _, err := st.AuthCodes().Consume(ctx, "code1", now); err != domain.ErrConsumed {
		t.Fatalf("authcode replay: %v", err)
	}

	if err := st.ActivationCodes().Create(ctx, domain.ActivationCode{
		Code: "act1", StakeCredentialHash: "h1", ChannelType: "telegram",
		Status: domain.ActivationActive, ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Wrong channel → ErrPurpose.
	if _, err := st.ActivationCodes().Consume(ctx, "act1", "discord", now); err != domain.ErrPurpose {
		t.Fatalf("wrong channel: %v", err)
	}
	got, err := st.ActivationCodes().Consume(ctx, "act1", "telegram", now)
	if err != nil || got.Status != domain.ActivationConsumed {
		t.Fatalf("consume activation: %v %+v", err, got)
	}
	if _, err := st.ActivationCodes().Consume(ctx, "act1", "telegram", now); err != domain.ErrConsumed {
		t.Fatalf("activation replay: %v", err)
	}
}
