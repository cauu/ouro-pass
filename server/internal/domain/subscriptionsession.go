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
	// GraceUntil is the membership-loss expiry deadline (S0019 p1-2). It is nil
	// whenever the session is not in grace (the sole "not in grace" signal); it is
	// set to `now + GRACE` the first reconcile that observes `state == none`, and
	// cleared back to nil the moment membership is re-observed. Distinct from the
	// informational ExpiresAt (= LastVerifiedAt + TTL), which is never enforced.
	GraceUntil  *time.Time
	CancelledAt *time.Time
}
