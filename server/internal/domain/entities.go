// Package domain holds the persisted entity types, enums, and shared errors for
// the issuer (detailed §1–§8). It depends on nothing in the project so every
// layer can import it. Per decision D6, money/time/json are carried as portable
// representations in storage; in Go they surface as the natural types below.
package domain

import (
	"errors"
	"time"
)

// ErrNotFound is returned by repositories when a row is absent.
var ErrNotFound = errors.New("not found")

// ---- §2 Pool & signing keys ----

// PoolConfig is the stake pool this issuer serves (usually a single row).
type PoolConfig struct {
	PoolID      string
	Ticker      string
	Name        *string
	MetadataURL *string
	Network     string // mainnet | preprod | preview
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

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
// chain — C9). The private key is stored encrypted (C5).
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
