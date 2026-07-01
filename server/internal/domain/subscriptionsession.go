package domain

import "time"

const (
	// SubscriptionTTL is the informational validity window (S0019): a member's
	// displayed ExpiresAt = LastVerifiedAt + TTL ("valid through, auto-renews"). It
	// is NEVER enforced — real expiry is membership-driven (grace + deadline). Shared
	// by the reconciler (slides it each pass) and the telegram activation display so
	// the two cannot drift (p3-5).
	SubscriptionTTL = 30 * 24 * time.Hour
	// SubscriptionGrace is the membership-loss grace: the first reconcile that sees
	// state==none records GraceUntil = now + GRACE and expiry is now >= GraceUntil.
	// ≈ 1 mainnet epoch, comfortably > one reconcile cadence, and < TTL.
	SubscriptionGrace = 5 * 24 * time.Hour
)

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
