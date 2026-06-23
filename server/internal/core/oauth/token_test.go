package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/utils/chain"
	"github.com/poolops/issuer/internal/utils/crypto"
	"github.com/poolops/issuer/internal/utils/jose"
)

// authCodeFor runs authorize and returns the plaintext code for the given sch.
func (h *harness) eligibleCode(t *testing.T) (string, string) {
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakeLovelace: "5000000"})
	code, err := h.authorizeAs(t, sch)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	return code, sch
}

func TestToken_AuthCodeConfidential(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// Give the client a secret.
	secret := "s3cr3t"
	h.st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential, Party: domain.FirstParty,
		ClientSecretHash: ptrStr(crypto.HashToken(secret)),
		RedirectURIs:     []string{"https://app/cb"}, AllowedAudiences: []string{"app:ouro"},
		AllowedScopes: []string{"read"}, Status: "active", CreatedAt: time.Now(),
	})
	code, sch := h.eligibleCode(t)

	resp, err := h.srv.Token(ctx, TokenRequest{
		GrantType: "authorization_code", Code: code, ClientID: "c1",
		ClientSecret: secret, RedirectURI: "https://app/cb",
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" || resp.Tier != "gold" || resp.TokenType != "Bearer" {
		t.Fatalf("response: %+v", resp)
	}

	// Access token verifies against published JWKS and carries derived sub/tier.
	pub, _ := h.srv.cfg.Keys.PublicJWKSKeys(ctx)
	jwks, _ := jose.BuildJWKS(pub)
	tok, err := jose.Verify(resp.AccessToken, jwks)
	if err != nil {
		t.Fatalf("verify access token: %v", err)
	}
	schBytes, _ := hex.DecodeString(sch)
	if tok.Subject() != crypto.DeriveSub([]byte("salt"), schBytes) {
		t.Errorf("sub mismatch")
	}
	if tier, _ := tok.Get("tier"); tier != "gold" {
		t.Errorf("tier claim = %v", tier)
	}

	// Ledger row + active refresh grant recorded.
	if _, err := h.st.RefreshGrants().Get(ctx, crypto.HashToken(resp.RefreshToken)); err != nil {
		t.Errorf("refresh grant not stored: %v", err)
	}

	// Wrong secret rejected.
	code2, _ := h.eligibleCode(t)
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code2, ClientID: "c1", ClientSecret: "wrong", RedirectURI: "https://app/cb"}); err != ErrInvalidClientCreds {
		t.Errorf("wrong secret: %v, want invalid_client", err)
	}
}

func TestToken_AuthCodePublicPKCE(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "spa", Name: "SPA", ClientType: domain.ClientPublic, Party: domain.ThirdParty,
		RedirectURIs: []string{"https://spa/cb"}, AllowedAudiences: []string{"app:ouro"},
		AllowedScopes: []string{"read"}, PKCERequired: true, Status: "active", CreatedAt: time.Now(),
	})
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakeLovelace: "5000000"})

	verifier := "the-pkce-code-verifier-string"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	nonce, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.vkey)
	code, err := h.srv.Authorize(ctx, AuthorizeRequest{
		ClientID: "spa", RedirectURI: "https://spa/cb", Aud: "app:ouro", Nonce: nonce,
		StakeVkey: h.vkey, Signature: h.sign(t, nonce), CodeChallenge: challenge,
	})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}

	// Correct verifier → success with cnf.jkt bound to device key.
	device := "aa" + hex.EncodeToString([]byte("device-public-key-bytes-32-xxxxx"))[2:]
	resp, err := h.srv.Token(ctx, TokenRequest{
		GrantType: "authorization_code", Code: code, ClientID: "spa",
		CodeVerifier: verifier, RedirectURI: "https://spa/cb", DevicePubkey: device,
	})
	if err != nil {
		t.Fatalf("token (public): %v", err)
	}
	pub, _ := h.srv.cfg.Keys.PublicJWKSKeys(ctx)
	jwks, _ := jose.BuildJWKS(pub)
	tok, _ := jose.Verify(resp.AccessToken, jwks)
	cnf, _ := tok.Get("cnf")
	if cnf == nil {
		t.Error("public client token should carry cnf (PoP)")
	}

	// Wrong verifier → invalid_grant.
	nonce2, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.vkey)
	code2, _ := h.srv.Authorize(ctx, AuthorizeRequest{
		ClientID: "spa", RedirectURI: "https://spa/cb", Aud: "app:ouro", Nonce: nonce2,
		StakeVkey: h.vkey, Signature: h.sign(t, nonce2), CodeChallenge: challenge,
	})
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code2, ClientID: "spa", CodeVerifier: "wrong", RedirectURI: "https://spa/cb"}); err != ErrInvalidGrant {
		t.Errorf("wrong PKCE verifier: %v, want invalid_grant", err)
	}
}

func TestToken_ReusedCodeRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential, Party: domain.FirstParty,
		ClientSecretHash: ptrStr(crypto.HashToken("s")), RedirectURIs: []string{"https://app/cb"},
		AllowedAudiences: []string{"app:ouro"}, AllowedScopes: []string{"read"}, Status: "active", CreatedAt: time.Now(),
	})
	code, _ := h.eligibleCode(t)
	r := TokenRequest{GrantType: "authorization_code", Code: code, ClientID: "c1", ClientSecret: "s", RedirectURI: "https://app/cb"}
	if _, err := h.srv.Token(ctx, r); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if _, err := h.srv.Token(ctx, r); err != ErrInvalidGrant {
		t.Errorf("code reuse: %v, want invalid_grant", err)
	}
}

func TestToken_UnsupportedGrant(t *testing.T) {
	h := newHarness(t)
	if _, err := h.srv.Token(context.Background(), TokenRequest{GrantType: "password"}); err != ErrUnsupportedGrant {
		t.Errorf("got %v, want unsupported_grant_type", err)
	}
}

func ptrStr(s string) *string { return &s }
