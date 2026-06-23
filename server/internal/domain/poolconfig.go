package domain

import "time"

// PoolConfig is the stake pool this issuer serves (usually a single row) (§2.1).
type PoolConfig struct {
	PoolID      string
	Ticker      string
	Name        *string
	MetadataURL *string
	Network     string // mainnet | preprod | preview
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
