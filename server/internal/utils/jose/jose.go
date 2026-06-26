// Package jose builds and verifies the service's signed tokens (access /
// activation) as compact JWS (EdDSA) and publishes the signing public keys as a
// JWKS — no certificate chain (spec C6/C9, detailed §9.2/§9.6). It wraps
// github.com/lestrrat-go/jwx/v2.
package jose

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jws"
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// TypAccessToken is the JOSE `typ` header for access tokens.
const TypAccessToken = "at+jwt"

// AccessClaims is the payload of an access token: an on-chain identity attestation
// (S0006 §2.3). It carries one self-describing entry per credential the subject
// holds in `credentials` — each with its kind and kind-specific facts (pool_stake:
// pool/network/state/active_stake_lovelace/epochs_active/member_since) — so relying
// parties read the entries they care about and apply their own policy; `tier` is an
// optional first-party opinion RPs may ignore.
type AccessClaims struct {
	Issuer    string // token `iss`: the issuer's deployment identity (S0006 D3), not pool-derived
	Subject   string // pseudonymous sub
	Audience  string
	IssuedAt  time.Time
	NotBefore time.Time
	Expiry    time.Time
	JTI       string

	// Credentials is the self-describing attestation set (S0006 §2.3): one entry per
	// held credential. Issued only when non-empty (the thin gate guarantees ≥1).
	Credentials []map[string]any
	Tier        string // optional first-party opinion (issuer tier_rules); "" → omitted

	Cnf map[string]string // optional PoP confirmation, e.g. {"jkt": "..."}
}

// SignAccessToken produces a compact JWS access token signed by (kid, priv).
func SignAccessToken(kid string, priv ed25519.PrivateKey, c AccessClaims) (string, error) {
	b := jwt.NewBuilder().
		Issuer(c.Issuer).
		Subject(c.Subject).
		Audience([]string{c.Audience}).
		IssuedAt(c.IssuedAt).
		NotBefore(c.NotBefore).
		Expiration(c.Expiry).
		JwtID(c.JTI)
	if len(c.Credentials) > 0 {
		b = b.Claim("credentials", c.Credentials)
	}
	if c.Tier != "" {
		b = b.Claim("tier", c.Tier)
	}
	if len(c.Cnf) > 0 {
		b = b.Claim("cnf", c.Cnf)
	}
	tok, err := b.Build()
	if err != nil {
		return "", err
	}
	return sign(kid, priv, TypAccessToken, tok)
}

func sign(kid string, priv ed25519.PrivateKey, typ string, tok jwt.Token) (string, error) {
	key, err := jwk.FromRaw(priv)
	if err != nil {
		return "", err
	}
	if err := key.Set(jwk.KeyIDKey, kid); err != nil {
		return "", err
	}
	hdr := jws.NewHeaders()
	_ = hdr.Set(jws.TypeKey, typ)
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.EdDSA, key, jws.WithProtectedHeaders(hdr)))
	if err != nil {
		return "", err
	}
	return string(signed), nil
}

// PublicKey describes one signing key for JWKS publication.
type PublicKey struct {
	KID    string
	Public ed25519.PublicKey
	Status string // active | rotating | retired
}

// BuildJWKS renders the public keys as a JWKS document (OKP/Ed25519), carrying
// only public keys and a per-key status — no certificate chain (detailed §9.6).
func BuildJWKS(keys []PublicKey) ([]byte, error) {
	set := jwk.NewSet()
	for _, k := range keys {
		jk, err := jwk.FromRaw(k.Public)
		if err != nil {
			return nil, err
		}
		_ = jk.Set(jwk.KeyIDKey, k.KID)
		_ = jk.Set(jwk.AlgorithmKey, jwa.EdDSA) // required by jwx keyset verification
		_ = jk.Set("status", k.Status)
		if err := set.AddKey(jk); err != nil {
			return nil, err
		}
	}
	return json.Marshal(set)
}

// JTIUnverified extracts the jti from a token WITHOUT verifying its signature
// or expiry. Used only for revocation, where an attacker presenting a token can
// only revoke that same token's ledger row (no privilege gained).
func JTIUnverified(tokenStr string) (string, error) {
	t, err := jwt.ParseInsecure([]byte(tokenStr))
	if err != nil {
		return "", err
	}
	return t.JwtID(), nil
}

// Verify parses and signature-verifies a token against a JWKS document, then
// returns the validated jwt.Token (used by introspect and TC-4). Standard
// time-based claims are validated.
func Verify(tokenStr string, jwksJSON []byte) (jwt.Token, error) {
	set, err := jwk.Parse(jwksJSON)
	if err != nil {
		return nil, fmt.Errorf("parse jwks: %w", err)
	}
	tok, err := jwt.Parse([]byte(tokenStr), jwt.WithKeySet(set), jwt.WithValidate(true))
	if err != nil {
		return nil, err
	}
	return tok, nil
}
