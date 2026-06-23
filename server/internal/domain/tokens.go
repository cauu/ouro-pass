package domain

import "time"

// ---- §4 Tokens & credentials ----

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

// ClientType distinguishes public (PKCE+DPoP) from confidential (client_secret).
type ClientType string

const (
	ClientPublic       ClientType = "public"
	ClientConfidential ClientType = "confidential"
)

// GrantStatus is the RefreshGrant lifecycle (rotation chain for theft detection).
type GrantStatus string

const (
	GrantActive  GrantStatus = "active"
	GrantRotated GrantStatus = "rotated"
	GrantRevoked GrantStatus = "revoked"
	GrantExpired GrantStatus = "expired"
)

// RefreshGrant is a long-lived, rotatable, revocable refresh credential (§4.2).
// refresh_grant_id stores a hash, never the plaintext.
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

// AuthorizationCode is the one-time OAuth code issued by /connect and redeemed
// at /api/oauth/token (§4.3). `code` stores a hash.
type AuthorizationCode struct {
	Code                string
	ClientID            string
	StakeCredentialHash string
	Aud                 string
	Scope               []string
	RedirectURI         string
	CodeChallenge       *string // PKCE S256
	ExpiresAt           time.Time
	ConsumedAt          *time.Time
	CreatedAt           time.Time
}

// ActivationStatus is the ActivationCode lifecycle.
type ActivationStatus string

const (
	ActivationActive   ActivationStatus = "active"
	ActivationConsumed ActivationStatus = "consumed"
	ActivationExpired  ActivationStatus = "expired"
)

// ActivationCode is the one-time channel binding code (§4.4). When implemented
// as a signed Activation Token, this row degrades to a consumed-jti record.
type ActivationCode struct {
	Code                string
	StakeCredentialHash string
	ChannelType         string
	Status              ActivationStatus
	ExpiresAt           time.Time
	ConsumedAt          *time.Time
	CreatedAt           time.Time
}

// NoncePurpose constrains AuthNonce usage. Member-auth nonces use issue/activation
// (detailed §9.3); admin uses admin_login/step_up (§9.8).
type NoncePurpose string

const (
	NonceIssue      NoncePurpose = "issue"
	NonceActivation NoncePurpose = "activation"
	NonceAdminLogin NoncePurpose = "admin_login"
	NonceStepUp     NoncePurpose = "step_up"
)

// AuthNonce is a one-time wallet-signing nonce (replay protection) (§4.5).
type AuthNonce struct {
	Nonce       string
	Purpose     NoncePurpose
	BoundKeyHash *string
	ExpiresAt   time.Time
	ConsumedAt  *time.Time
	CreatedAt   time.Time
}
