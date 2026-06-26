package domain

import "time"

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

// PushJob is a broadcast task targeting topic/entitlement/tier (§7.1). S0005:
// ChannelID, when set, scopes delivery to a single channel instance and routes
// the send through that instance's transport (D5); nil keeps the legacy
// type-level fan-out for back-compat.
type PushJob struct {
	JobID               string
	PoolID              string
	Title               string
	Content             string
	ChannelID           *string
	ChannelType         string
	TargetTopic         *string
	RequiredEntitlement *string
	TargetTier          *string
	Status              PushJobStatus
	ScheduledAt         *time.Time
	CreatedBy           string // admin_id
	CreatedAt           time.Time
}
