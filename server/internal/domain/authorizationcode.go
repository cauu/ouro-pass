package domain

import "time"

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
