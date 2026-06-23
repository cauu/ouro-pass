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

// ClientParty distinguishes first-party from third-party integrations.
type ClientParty string

const (
	FirstParty ClientParty = "first_party"
	ThirdParty ClientParty = "third_party"
)

// Valid reports whether the party is a known value.
func (p ClientParty) Valid() bool { return p == FirstParty || p == ThirdParty }

// OAuthClient is a registered integration application (§5.1).
type OAuthClient struct {
	ClientID         string
	Name             string
	ClientType       ClientType // public | confidential
	ClientSecretHash *string    // 🔒 confidential only
	Party            ClientParty
	RedirectURIs     []string // exact-match allowlist
	AllowedAudiences []string
	AllowedScopes    []string
	PKCERequired     bool
	Status           string // active | disabled
	CreatedAt        time.Time
}
