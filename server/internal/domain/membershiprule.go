package domain

import (
	"encoding/json"
	"time"
)

// RuleStatus enables/disables a membership rule.
type RuleStatus string

const (
	RuleActive   RuleStatus = "active"
	RuleDisabled RuleStatus = "disabled"
)

// Valid reports whether the rule status is a known value.
func (s RuleStatus) Valid() bool { return s == RuleActive || s == RuleDisabled }

// MembershipRule defines a tier's eligibility threshold and granted
// entitlements. Match conditions live in the opaque RuleConfig JSON so rules
// evolve without schema migrations (§3.1). No Member table — identity is the
// stake credential hash held directly by durable rows (C8).
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
