package domain

import "time"

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
