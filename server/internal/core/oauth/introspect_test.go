package oauth

import (
	"context"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/crypto"
)

// issuedTokenPair runs a full confidential exchange and returns access+refresh.
func issuedTokenPair(t *testing.T) (*harness, string, string) {
	h := newHarness(t)
	ctx := context.Background()
	h.st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential, Party: domain.FirstParty,
		ClientSecretHash: ptrStr(crypto.HashToken("s")), RedirectURIs: []string{"https://app/cb"},
		AllowedAudiences: []string{"app:ouro"}, AllowedScopes: []string{"read"}, Status: "active", CreatedAt: time.Now(),
	})
	code, _ := h.eligibleCode(t)
	resp, err := h.srv.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code, ClientID: "c1", ClientSecret: "s", RedirectURI: "https://app/cb"})
	if err != nil {
		t.Fatal(err)
	}
	return h, resp.AccessToken, resp.RefreshToken
}

func TestIntrospect_ActiveToken(t *testing.T) {
	h, access, _ := issuedTokenPair(t)
	res, err := h.srv.Introspect(context.Background(), access, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Active || res.Tier != "gold" || res.Sub == "" || res.MembershipStatus != "eligible_member" {
		t.Fatalf("introspect active: %+v", res)
	}
}

func TestIntrospect_RevokedAndUnknown(t *testing.T) {
	h, access, _ := issuedTokenPair(t)
	ctx := context.Background()

	// Revoke via the access token, then introspect → inactive.
	if err := h.srv.Revoke(ctx, access, ""); err != nil {
		t.Fatal(err)
	}
	res, _ := h.srv.Introspect(ctx, access, "")
	if res.Active {
		t.Fatal("revoked token should be inactive")
	}
	// Unknown jti → inactive, no error.
	res, _ = h.srv.Introspect(ctx, "", "no-such-jti")
	if res.Active {
		t.Fatal("unknown jti should be inactive")
	}
	// Garbage token → inactive, no error.
	res, _ = h.srv.Introspect(ctx, "not.a.jwt", "")
	if res.Active {
		t.Fatal("garbage token should be inactive")
	}
}

func TestRevoke_RefreshToken(t *testing.T) {
	h, _, refresh := issuedTokenPair(t)
	ctx := context.Background()
	if err := h.srv.Revoke(ctx, refresh, "refresh_token"); err != nil {
		t.Fatal(err)
	}
	g, _ := h.st.RefreshGrants().Get(ctx, crypto.HashToken(refresh))
	if g.Status != domain.GrantRevoked {
		t.Fatalf("refresh grant status = %s, want revoked", g.Status)
	}
	// A revoked refresh token can no longer mint tokens.
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: refresh, ClientID: "c1", ClientSecret: "s"}); err != ErrInvalidGrant {
		t.Errorf("revoked refresh use: %v, want invalid_grant", err)
	}
}

func TestRevoke_UnknownTokenSucceeds(t *testing.T) {
	h, _, _ := issuedTokenPair(t)
	// RFC 7009: revoking an unknown token still returns success (nil error).
	if err := h.srv.Revoke(context.Background(), "unknown-opaque-token", "refresh_token"); err != nil {
		t.Errorf("unknown revoke: %v, want nil", err)
	}
}
