package domain

import "time"

// ---- §7 Push ----

// PushJobStatus is the PushJob lifecycle.
type PushJobStatus string

const (
	PushDraft     PushJobStatus = "draft"
	PushScheduled PushJobStatus = "scheduled"
	PushRunning   PushJobStatus = "running"
	PushDone      PushJobStatus = "done"
	PushCancelled PushJobStatus = "cancelled"
	PushFailed    PushJobStatus = "failed"
)

// PushJob is a broadcast task targeting topic/entitlement/tier (§7.1).
type PushJob struct {
	JobID               string
	PoolID              string
	Title               string
	Content             string
	ChannelType         string
	TargetTopic         *string
	RequiredEntitlement *string
	TargetTier          *string
	Status              PushJobStatus
	ScheduledAt         *time.Time
	CreatedBy           string // admin_id
	CreatedAt           time.Time
}

// DeliveryStatus is a per-recipient delivery outcome.
type DeliveryStatus string

const (
	DeliverySent    DeliveryStatus = "sent"
	DeliveryFailed  DeliveryStatus = "failed"
	DeliverySkipped DeliveryStatus = "skipped"
)

// DeliveryLog is one delivery record per recipient (§7.2).
type DeliveryLog struct {
	DeliveryID    string
	JobID         string
	SessionID     string
	ChannelType   string
	ChannelUserID string
	Status        DeliveryStatus
	RetryCount    int
	ErrorMessage  *string
	SentAt        *time.Time
}

// ---- §8 Admin & audit ----

// AdminRole is the RBAC role for an admin user.
type AdminRole string

const (
	RoleOwner    AdminRole = "owner"
	RoleOperator AdminRole = "operator"
	RoleViewer   AdminRole = "viewer"
)

// AdminUser is a backend administrator; identity is an owner stake key
// (wallet-signature login). The `owner` role's key must be in the on-chain pool
// owner list (§8.1, C9).
type AdminUser struct {
	AdminID      string
	PoolID       string
	OwnerKeyHash string
	Role         AdminRole
	LastLoginAt  *time.Time
	CreatedAt    time.Time
}

// AdminSession is a server-side admin session (httpOnly cookie; token stored
// hashed) (§8.2).
type AdminSession struct {
	SessionToken string // stored as hash
	AdminID      string
	ExpiresAt    time.Time
	IP           *string
	CreatedAt    time.Time
}

// AuditLog is the audit trail of sensitive operations (§8.3).
type AuditLog struct {
	AuditID    string
	Actor      string // admin_id or "system"
	Action     string
	Target     string
	BeforeHash *string
	AfterHash  *string
	IP         *string
	CreatedAt  time.Time
}
