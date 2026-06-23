package domain

import "time"

// GrantStatus is the RefreshGrant lifecycle (rotation chain for theft detection).
type GrantStatus string

const (
	GrantActive  GrantStatus = "active"
	GrantRotated GrantStatus = "rotated"
	GrantRevoked GrantStatus = "revoked"
	GrantExpired GrantStatus = "expired"
)

// RefreshGrant is a long-lived, rotatable, revocable refresh credential (§4.2).
// refresh_grant_id stores a hash, never the plaintext. ClientType is defined
// with OAuthClient.
type RefreshGrant struct {
	RefreshGrantID      string
	StakeCredentialHash string
	Audience            string
	ClientType          ClientType
	BoundDevicePubkey   []byte // public-client PoP device key
	ClientID            *string
	Status              GrantStatus
	RotatedFrom         *string
	CreatedAt           time.Time
	ExpiresAt           *time.Time
	LastUsedAt          *time.Time
}
