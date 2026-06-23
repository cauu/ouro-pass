package domain

import "time"

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
