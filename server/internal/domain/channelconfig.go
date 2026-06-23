package domain

import (
	"encoding/json"
	"time"
)

// ChannelConfig is an SPO-configured channel instance; secret subfields in
// Config are encrypted by the channel handler before storage (🔒, §6.1).
type ChannelConfig struct {
	ChannelID   string
	PoolID      string
	ChannelType string // telegram | discord | email | webhook
	Config      json.RawMessage
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
