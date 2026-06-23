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
