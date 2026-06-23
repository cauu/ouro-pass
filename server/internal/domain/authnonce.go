package domain

import "time"

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
	Nonce        string
	Purpose      NoncePurpose
	BoundKeyHash *string
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
	CreatedAt    time.Time
}
