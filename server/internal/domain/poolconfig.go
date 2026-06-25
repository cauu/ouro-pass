package domain

import (
	"encoding/json"
	"time"
)

// PoolConfig is the stake pool this issuer serves (usually a single row) (§2.1).
type PoolConfig struct {
	PoolID      string
	Ticker      string
	Name        *string
	MetadataURL *string
	Network     string // mainnet | preprod | preview
	// TierRules is the issuer's thin first-party tier mapping (S0004 §2.6, D6): an
	// ordered JSON array of {tier, min_state, min_active_stake}, first match wins.
	// Consumed ONLY by the issuer's own channels (Telegram/Push); external RPs read
	// the raw token facts and apply their own policy. Empty/[] → no tier opinion.
	TierRules   json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
