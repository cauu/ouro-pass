package domain

import (
	"encoding/json"
	"time"
)

// Attestor status values.
const (
	AttestorActive   = "active"
	AttestorDisabled = "disabled"
)

// AttestorConfig is one configured on-chain identity credential source (S0006
// D2): the generalization of "the served pool". Kind selects the runtime
// evaluator (pool_stake | nft…); Params is the kind-specific JSON config
// (pool_stake: {pool_id, network, ticker, name}). Label is a human-readable name,
// unique within a Kind. Status gates whether the attestor is evaluated/served.
type AttestorConfig struct {
	AttestorID string
	Kind       string
	Label      string
	Params     json.RawMessage
	Status     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
