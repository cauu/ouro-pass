package domain

import (
	"encoding/json"
	"time"
)

// ValidChannelType reports whether s is a supported channel type.
func ValidChannelType(s string) bool {
	switch s {
	case "telegram", "discord", "email", "webhook":
		return true
	}
	return false
}

// ChannelConfig is an SPO-configured channel instance; secret subfields in
// Config are encrypted by the channel handler before storage (🔒, §6.1).
//
// S0005: an instance is a first-class addressable entity. ChannelID is stable
// from creation, and Name is the human-readable label unique within a
// (PoolID, ChannelType) — a pool may run N active instances of one platform.
type ChannelConfig struct {
	ChannelID   string
	PoolID      string
	ChannelType string // telegram | discord | email | webhook
	Name        string // instance label, unique per (pool_id, channel_type)
	Config      json.RawMessage
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
