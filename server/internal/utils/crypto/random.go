package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// RandomID returns a 128-bit random identifier as hex (session/job/channel ids).
func RandomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RandomToken returns nbytes of CSPRNG output as URL-safe base64 without padding
// — used for opaque codes, refresh-grant plaintext, and nonces.
func RandomToken(nbytes int) string {
	b := make([]byte, nbytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the SHA-256 hex digest of a token's plaintext. Opaque
// credentials (codes, refresh grants, admin sessions) are stored hashed.
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
