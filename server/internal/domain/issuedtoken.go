package domain

import "time"

// TokenKind distinguishes ledger rows for access vs activation tokens.
type TokenKind string

const (
	TokenAccess     TokenKind = "access"
	TokenActivation TokenKind = "activation"
)

// TokenStatus is the IssuedToken lifecycle.
type TokenStatus string

const (
	TokenActive  TokenStatus = "active"
	TokenExpired TokenStatus = "expired"
	TokenRevoked TokenStatus = "revoked"
)

// IssuedToken is the ledger of issued tokens (introspect/revoke/audit; the
// token body itself is not stored) (§4.1).
type IssuedToken struct {
	JTI                 string
	StakeCredentialHash string
	Kind                TokenKind
	Audience            string
	KID                 string
	ClientID            *string
	Status              TokenStatus
	IssuedAt            time.Time
	ExpiresAt           time.Time
	RedeemedAt          *time.Time
	RevokedAt           *time.Time
}
