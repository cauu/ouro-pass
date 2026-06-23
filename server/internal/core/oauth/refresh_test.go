package oauth

import (
	"context"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
)

// confidentialHarness returns a harness whose client c1 has secret "s" and an
// initial token grant, returning the first refresh token and the sch.
func confidentialHarness(t *testing.T) (*harness, string, string) {
	h := newHarness(t)
	ctx := context.Background()
	h.st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential, Party: domain.FirstParty,
		ClientSecretHash: ptrStr(crypto.HashToken("s")), RedirectURIs: []string{"https://app/cb"},
		AllowedAudiences: []string{"app:ouro"}, AllowedScopes: []string{"read"}, Status: "active", CreatedAt: time.Now(),
	})
	code, sch := h.eligibleCode(t)
	resp, err := h.srv.Token(ctx, TokenRequest{
		GrantType: "authorization_code", Code: code, ClientID: "c1", ClientSecret: "s", RedirectURI: "https://app/cb",
	})
	if err != nil {
		t.Fatalf("initial token: %v", err)
	}
	return h, resp.RefreshToken, sch
}

func TestRefresh_RotatesAndIssuesNew(t *testing.T) {
	h, refresh1, _ := confidentialHarness(t)
	ctx := context.Background()

	resp, err := h.srv.Token(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: refresh1, ClientID: "c1", ClientSecret: "s"})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" || resp.RefreshToken == refresh1 {
		t.Fatalf("rotation should yield a new refresh token: %+v", resp)
	}
	// Old grant is now rotated; new grant active with rotated_from set.
	old, _ := h.st.RefreshGrants().Get(ctx, crypto.HashToken(refresh1))
	if old.Status != domain.GrantRotated {
		t.Errorf("old grant status = %s, want rotated", old.Status)
	}
	newG, _ := h.st.RefreshGrants().Get(ctx, crypto.HashToken(resp.RefreshToken))
	if newG.Status != domain.GrantActive || newG.RotatedFrom == nil {
		t.Errorf("new grant: %+v", newG)
	}
}

func TestRefresh_ReplayRevokesChain(t *testing.T) {
	h, refresh1, _ := confidentialHarness(t)
	ctx := context.Background()

	// Rotate once → refresh2.
	resp, err := h.srv.Token(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: refresh1, ClientID: "c1", ClientSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	refresh2 := resp.RefreshToken

	// Replay the now-rotated refresh1 → invalid_grant AND chain revoked.
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: refresh1, ClientID: "c1", ClientSecret: "s"}); err != ErrInvalidGrant {
		t.Fatalf("replay: %v, want invalid_grant", err)
	}
	// refresh2 (descendant) must now be revoked by the chain revoke.
	g2, _ := h.st.RefreshGrants().Get(ctx, crypto.HashToken(refresh2))
	if g2.Status != domain.GrantRevoked {
		t.Fatalf("descendant grant status = %s, want revoked (theft response)", g2.Status)
	}
	// And refresh2 can no longer be used.
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: refresh2, ClientID: "c1", ClientSecret: "s"}); err != ErrInvalidGrant {
		t.Errorf("revoked refresh2: %v, want invalid_grant", err)
	}
}

func TestRefresh_ReEvaluatesEligibility(t *testing.T) {
	h, refresh1, sch := confidentialHarness(t)
	ctx := context.Background()

	// Member moves delegation away → no longer eligible → refresh denied.
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 481, DelegatedPoolID: "pool1other", ActiveStakeLovelace: "5000000"})
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: refresh1, ClientID: "c1", ClientSecret: "s"}); err != ErrNotEligible {
		t.Fatalf("ineligible refresh: %v, want not_eligible", err)
	}
}

func TestRefresh_WrongSecretAndUnknownGrant(t *testing.T) {
	h, refresh1, _ := confidentialHarness(t)
	ctx := context.Background()
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: refresh1, ClientID: "c1", ClientSecret: "wrong"}); err != ErrInvalidClientCreds {
		t.Errorf("wrong secret: %v, want invalid_client", err)
	}
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: "nonexistent", ClientID: "c1", ClientSecret: "s"}); err != ErrInvalidGrant {
		t.Errorf("unknown grant: %v, want invalid_grant", err)
	}
}
