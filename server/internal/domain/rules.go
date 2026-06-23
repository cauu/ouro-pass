package domain

import (
	"encoding/json"
	"time"
)

// ---- §3 Rules & identity (no Member table — C8; identity key is the stake
// credential hash held directly by durable rows) ----

// RuleStatus enables/disables a membership rule.
type RuleStatus string

const (
	RuleActive   RuleStatus = "active"
	RuleDisabled RuleStatus = "disabled"
)

// MembershipRule defines a tier's eligibility threshold and granted
// entitlements. Match conditions live in the opaque RuleConfig JSON so rules
// evolve without schema migrations (§3.1).
type MembershipRule struct {
	RuleID       string
	Name         string
	RuleConfig   json.RawMessage // {required_status,min_active_stake_lovelace,min_active_epochs,grace_epochs,...}
	Tier         string
	Entitlements []string
	Priority     int
	Status       RuleStatus
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// StakeSnapshotCache caches raw on-chain stake snapshots (not eligibility
// conclusions); eligibility is recomputed by the rule engine (§3.3). Optional.
type StakeSnapshotCache struct {
	StakeCredentialHash string
	SnapshotEpoch       int64
	DelegatedPoolID     *string
	ActiveStakeLovelace *string // numeric(20) carried as decimal string (C4/D6)
	RewardsLovelace     *string
	Source              string // node_lsq | db_sync | koios | blockfrost
	FetchedAt           time.Time
}

// Blacklist is the sparse manual deny-list keyed by stake credential (§3.2).
type Blacklist struct {
	StakeCredentialHash string
	Reason              *string
	CreatedAt           time.Time
}
