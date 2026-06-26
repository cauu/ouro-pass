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
// state (§6.2). S0005: bound to a specific channel instance via ChannelID, and
// unique on (channel_id, channel_user_id) so one channel user may subscribe to
// several instances of the same platform independently.
type SubscriptionSession struct {
	SessionID           string
	PoolID              string
	StakeCredentialHash string
	ChannelID           string
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
