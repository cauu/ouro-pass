package domain

import (
	"encoding/json"
	"time"
)

// ---- §5 Clients ----

// ClientParty distinguishes first-party from third-party integrations.
type ClientParty string

const (
	FirstParty ClientParty = "first_party"
	ThirdParty ClientParty = "third_party"
)

// OAuthClient is a registered integration application (§5.1).
type OAuthClient struct {
	ClientID         string
	Name             string
	ClientType       ClientType // public | confidential
	ClientSecretHash *string    // 🔒 confidential only
	Party            ClientParty
	RedirectURIs     []string // exact-match allowlist
	AllowedAudiences []string
	AllowedScopes    []string
	PKCERequired     bool
	Status           string // active | disabled
	CreatedAt        time.Time
}

// ---- §6 Channels & subscriptions ----

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
