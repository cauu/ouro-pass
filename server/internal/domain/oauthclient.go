package domain

import "time"

// ClientType distinguishes public (PKCE+DPoP) from confidential (client_secret)
// clients. Used by OAuthClient and RefreshGrant.
type ClientType string

const (
	ClientPublic       ClientType = "public"
	ClientConfidential ClientType = "confidential"
)

// Valid reports whether the client type is a known value.
func (c ClientType) Valid() bool { return c == ClientPublic || c == ClientConfidential }

// OAuthClient is a registered integration application (§5.1).
type OAuthClient struct {
	ClientID         string
	Name             string
	ClientType       ClientType // public | confidential
	ClientSecretHash *string    // 🔒 confidential only
	RedirectURIs     []string   // exact-match allowlist
	AllowedAudiences []string
	PKCERequired     bool
	Status           string // active | disabled
	CreatedAt        time.Time
}
