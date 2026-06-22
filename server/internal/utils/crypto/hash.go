// Package crypto holds the security primitives that anchor the service's trust:
// Blake2b-224 hashing (pool_id / credential), the pseudonymous `sub` derivation,
// AES-256-GCM field encryption for 🔒 columns, and CIP-30 COSE_Sign1 wallet
// signature verification (CIP-8). It is deliberately isolated so the audit
// surface is one package (spec §2.2). All functions are stateless.
package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"

	"golang.org/x/crypto/blake2b"
)

// Blake2b224 returns the 28-byte Blake2b-224 digest used for Cardano key hashes
// (pool_id = blake2b224(cold_vkey); stake credential = blake2b224(stake_vkey)).
func Blake2b224(data []byte) []byte {
	h, _ := blake2b.New(28, nil) // err only on invalid size/key; 28 is valid
	h.Write(data)
	return h.Sum(nil)
}

// subEncoding is lowercase base32 without padding — URL/JWT friendly, stable.
var subEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// DeriveSub computes the outward pseudonymous subject for a stake credential:
// base32(HMAC-SHA256(serverSalt, stakeCredentialHash)) (spec C8). It is
// deterministic (same inputs → same sub) and irreversible without the salt, so
// verifiers cannot recover the on-chain hash.
func DeriveSub(serverSalt, stakeCredentialHash []byte) string {
	mac := hmac.New(sha256.New, serverSalt)
	mac.Write(stakeCredentialHash)
	return subEncoding.EncodeToString(mac.Sum(nil))
}
