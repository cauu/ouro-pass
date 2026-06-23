package domain

import "time"

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
