package store

import (
	"context"

	"github.com/poolops/issuer/internal/domain"
)

// DeliveryLogRepo persists per-recipient delivery records (§7.2).
type DeliveryLogRepo struct{ s *Store }

// DeliveryLogs returns a repo bound to this store.
func (s *Store) DeliveryLogs() *DeliveryLogRepo { return &DeliveryLogRepo{s} }

// Append records one delivery outcome.
func (r *DeliveryLogRepo) Append(ctx context.Context, d domain.DeliveryLog) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO DeliveryLog (delivery_id, job_id, session_id, channel_type, channel_user_id, status, retry_count, error_message, sent_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		d.DeliveryID, d.JobID, d.SessionID, d.ChannelType, d.ChannelUserID, string(d.Status), d.RetryCount, nullStr(d.ErrorMessage), tsPtr(d.SentAt))
	return err
}

// CountByStatus returns the number of deliveries for a job in the given status.
func (r *DeliveryLogRepo) CountByStatus(ctx context.Context, jobID string, status domain.DeliveryStatus) (int, error) {
	var n int
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`SELECT COUNT(1) FROM DeliveryLog WHERE job_id = ? AND status = ?`),
		jobID, string(status)).Scan(&n)
	return n, err
}
