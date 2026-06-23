package domain

import "time"

// Blacklist is the sparse manual deny-list keyed by stake credential (§3.2).
type Blacklist struct {
	StakeCredentialHash string
	Reason              *string
	CreatedAt           time.Time
}
