package jose

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignAccessToken_VerifiableViaJWKS(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	const kid = "pao-issuer-2026-08"
	now := time.Now()

	tokenStr, err := SignAccessToken(kid, priv, AccessClaims{
		Issuer:       "poolops:pool1abc",
		Subject:      "sub-xyz",
		Audience:     "app:ouro-ops",
		IssuedAt:     now,
		NotBefore:    now,
		Expiry:       now.Add(24 * time.Hour),
		JTI:          "jti-1",
		Tier:         "gold",
		Entitlements: []string{"read", "push"},
		Cnf:          map[string]string{"jkt": "thumb"},
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// TC-4: header has kid + typ, and NO cert_hash / x5c.
	hdr := decodeHeader(t, tokenStr)
	if hdr["kid"] != kid {
		t.Errorf("kid = %v, want %s", hdr["kid"], kid)
	}
	if hdr["typ"] != TypAccessToken {
		t.Errorf("typ = %v, want %s", hdr["typ"], TypAccessToken)
	}
	if hdr["alg"] != "EdDSA" {
		t.Errorf("alg = %v, want EdDSA", hdr["alg"])
	}
	for _, banned := range []string{"cert_hash", "x5c", "x5t"} {
		if _, ok := hdr[banned]; ok {
			t.Errorf("header must not contain %q (no cert chain)", banned)
		}
	}

	// Independent verify via JWKS built from the public key.
	jwks, err := BuildJWKS([]PublicKey{{KID: kid, Public: pub, Status: "active"}})
	if err != nil {
		t.Fatalf("jwks: %v", err)
	}
	tok, err := Verify(tokenStr, jwks)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if tok.Subject() != "sub-xyz" {
		t.Errorf("sub = %q", tok.Subject())
	}
	tier, _ := tok.Get("tier")
	if tier != "gold" {
		t.Errorf("tier = %v", tier)
	}

	// Wrong key → verification fails.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	badJWKS, _ := BuildJWKS([]PublicKey{{KID: kid, Public: otherPub, Status: "active"}})
	if _, err := Verify(tokenStr, badJWKS); err == nil {
		t.Error("verify must fail with wrong public key")
	}
}

func TestBuildJWKS_OKPOnlyNoCertChain(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	jwks, err := BuildJWKS([]PublicKey{{KID: "k1", Public: pub, Status: "active"}})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := json.Unmarshal(jwks, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(doc.Keys))
	}
	k := doc.Keys[0]
	if k["kty"] != "OKP" || k["crv"] != "Ed25519" {
		t.Errorf("kty/crv = %v/%v, want OKP/Ed25519", k["kty"], k["crv"])
	}
	if k["kid"] != "k1" || k["status"] != "active" {
		t.Errorf("kid/status = %v/%v", k["kid"], k["status"])
	}
	if _, ok := k["d"]; ok {
		t.Error("JWKS leaked private key material (d)")
	}
	for _, banned := range []string{"x5c", "x5t", "chain"} {
		if _, ok := k[banned]; ok {
			t.Errorf("JWKS must not contain %q", banned)
		}
	}
}

func TestSignActivationToken(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s, err := SignActivationToken("k1", priv, ActivationClaims{
		Issuer: "poolops:pool1", Subject: "sub", ChannelType: "telegram",
		Tier: "gold", Entitlements: []string{"news"}, JTI: "a1",
		Expiry: time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := BuildJWKS([]PublicKey{{KID: "k1", Public: pub, Status: "active"}})
	tok, err := Verify(s, jwks)
	if err != nil {
		t.Fatal(err)
	}
	if ot, _ := tok.Get("one_time"); ot != true {
		t.Errorf("one_time = %v, want true", ot)
	}
	if ch, _ := tok.Get("channel_type"); ch != "telegram" {
		t.Errorf("channel_type = %v", ch)
	}
}

func decodeHeader(t *testing.T, jws string) map[string]any {
	t.Helper()
	parts := strings.Split(jws, ".")
	if len(parts) != 3 {
		t.Fatalf("not a compact JWS: %d parts", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var hdr map[string]any
	if err := json.Unmarshal(raw, &hdr); err != nil {
		t.Fatalf("header json: %v", err)
	}
	return hdr
}
