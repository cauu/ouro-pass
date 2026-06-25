package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
	"ouro-pass/server/internal/utils/jose"
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
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential,
		ClientSecretHash: ptrStr(crypto.HashToken(secret)),
		RedirectURIs:     []string{"https://app/cb"}, AllowedAudiences: []string{"app:ouro"},
		Status: "active", CreatedAt: time.Now(),
	})
	code, sch := h.eligibleCode(t)

	resp, err := h.srv.Token(ctx, TokenRequest{
		GrantType: "authorization_code", Code: code, ClientID: "c1",
		ClientSecret: secret, CodeVerifier: testPKCEVerifier, RedirectURI: "https://app/cb",
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
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code2, ClientID: "c1", ClientSecret: "wrong", CodeVerifier: testPKCEVerifier, RedirectURI: "https://app/cb"}); err != ErrInvalidClientCreds {
		t.Errorf("wrong secret: %v, want invalid_client", err)
	}

	// PKCE is mandatory for confidential clients too (p6-3): a correct secret but
	// NO code_verifier must be rejected. Guards against regressing to the old
	// secret-only path that skipped PKCE.
	code3, _ := h.eligibleCode(t)
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code3, ClientID: "c1", ClientSecret: secret, RedirectURI: "https://app/cb"}); err != ErrInvalidGrant {
		t.Errorf("confidential without code_verifier: %v, want invalid_grant", err)
	}
}

func TestToken_AuthCodePublicPKCE(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "spa", Name: "SPA", ClientType: domain.ClientPublic,
		RedirectURIs: []string{"https://spa/cb"}, AllowedAudiences: []string{"app:ouro"},
		Status: "active", CreatedAt: time.Now(),
	})
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakeLovelace: "5000000"})

	verifier := "the-pkce-code-verifier-string"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	nonce, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.rewardAddr)
	code, err := h.srv.Authorize(ctx, AuthorizeRequest{
		ClientID: "spa", RedirectURI: "https://spa/cb", Aud: "app:ouro", Nonce: nonce,
		CoseKey: h.coseKey, Signature: h.sign(t, nonce), CodeChallenge: challenge,
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
	nonce2, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.rewardAddr)
	code2, _ := h.srv.Authorize(ctx, AuthorizeRequest{
		ClientID: "spa", RedirectURI: "https://spa/cb", Aud: "app:ouro", Nonce: nonce2,
		CoseKey: h.coseKey, Signature: h.sign(t, nonce2), CodeChallenge: challenge,
	})
	if _, err := h.srv.Token(ctx, TokenRequest{GrantType: "authorization_code", Code: code2, ClientID: "spa", CodeVerifier: "wrong", RedirectURI: "https://spa/cb"}); err != ErrInvalidGrant {
		t.Errorf("wrong PKCE verifier: %v, want invalid_grant", err)
	}
}

// TestToken_PublicDevicePoP covers p12-5: a malformed device key is rejected at
// mint (no silent unbound token), and a device-bound public refresh must present
// the matching device key (TC-17).
func TestToken_PublicDevicePoP(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "spa", Name: "SPA", ClientType: domain.ClientPublic,
		RedirectURIs: []string{"https://spa/cb"}, AllowedAudiences: []string{"app:ouro"},
		Status: "active", CreatedAt: time.Now(),
	})
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakeLovelace: "5000000"})
	verifier := "the-pkce-code-verifier-string"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	device := hex.EncodeToString([]byte("device-public-key-bytes-32-xxxxx")) // 32 bytes

	mkCode := func() string {
		nonce, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.rewardAddr)
		c, err := h.srv.Authorize(ctx, AuthorizeRequest{
			ClientID: "spa", RedirectURI: "https://spa/cb", Aud: "app:ouro", Nonce: nonce,
			CoseKey: h.coseKey, Signature: h.sign(t, nonce), CodeChallenge: challenge,
		})
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		return c
	}

	// Malformed device key at mint → invalid_request (not a silent unbound token).
	if _, err := h.srv.Token(ctx, TokenRequest{
		GrantType: "authorization_code", Code: mkCode(), ClientID: "spa",
		CodeVerifier: verifier, RedirectURI: "https://spa/cb", DevicePubkey: "zz-not-hex",
	}); err != ErrInvalidRequest {
		t.Fatalf("malformed device: %v, want invalid_request", err)
	}

	// Valid device → token + refresh; then a device-bound refresh must match.
	resp, err := h.srv.Token(ctx, TokenRequest{
		GrantType: "authorization_code", Code: mkCode(), ClientID: "spa",
		CodeVerifier: verifier, RedirectURI: "https://spa/cb", DevicePubkey: device,
	})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	// Refresh without the device key → invalid_grant.
	if _, err := h.srv.Token(ctx, TokenRequest{
		GrantType: "refresh_token", RefreshToken: resp.RefreshToken, ClientID: "spa",
	}); err != ErrInvalidGrant {
		t.Fatalf("refresh missing device: %v, want invalid_grant", err)
	}
	// Refresh with the matching device key → success.
	if _, err := h.srv.Token(ctx, TokenRequest{
		GrantType: "refresh_token", RefreshToken: resp.RefreshToken, ClientID: "spa", DevicePubkey: device,
	}); err != nil {
		t.Fatalf("refresh with device: %v", err)
	}
}

func TestToken_ReusedCodeRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential,
		ClientSecretHash: ptrStr(crypto.HashToken("s")), RedirectURIs: []string{"https://app/cb"},
		AllowedAudiences: []string{"app:ouro"}, Status: "active", CreatedAt: time.Now(),
	})
	code, _ := h.eligibleCode(t)
	r := TokenRequest{GrantType: "authorization_code", Code: code, ClientID: "c1", ClientSecret: "s", CodeVerifier: testPKCEVerifier, RedirectURI: "https://app/cb"}
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
