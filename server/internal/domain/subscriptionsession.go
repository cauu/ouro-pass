package domain

import "time"

// SubscriptionStatus is the SubscriptionSession lifecycle.
type SubscriptionStatus string

const (
	SubActive     SubscriptionStatus = "active"
	SubDowngraded SubscriptionStatus = "downgraded"
	SubCancelled  SubscriptionStatus = "cancelled"
	SubExpired    SubscriptionStatus = "expired"
)

// SubscriptionSession is a member's subscription on a channel — server-side
// state (§6.2). Unique on (pool_id, channel_type, channel_user_id).
type SubscriptionSession struct {
	SessionID           string
	PoolID              string
	StakeCredentialHash string
	ChannelType         string
	ChannelUserID       string
	ChannelAccountID    *string
	Status              SubscriptionStatus
	Tier                string
	Topics              []string
	Entitlements        []string
	CreatedAt           time.Time
	LastVerifiedAt      time.Time
	ExpiresAt           time.Time
	CancelledAt         *time.Time
}
