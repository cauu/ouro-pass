package domain

import "time"

// IssuerKeyStatus is the lifecycle state of an L2 signing key.
type IssuerKeyStatus string

const (
	KeyPending  IssuerKeyStatus = "pending"
	KeyActive   IssuerKeyStatus = "active"
	KeyRotating IssuerKeyStatus = "rotating"
	KeyRetired  IssuerKeyStatus = "retired"
	KeyRevoked  IssuerKeyStatus = "revoked"
)

// IssuerKey is the issuer's rotatable Ed25519 signing key (no certificate
// chain — C9). The private key is stored encrypted (C5) (§2.2).
type IssuerKey struct {
	KID                 string
	PublicKey           []byte
	EncryptedPrivateKey []byte
	Status              IssuerKeyStatus
	ValidFrom           *time.Time
	ValidUntil          *time.Time
	CreatedAt           time.Time
	RetiredAt           *time.Time
}
