package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"ouro-pass/server/internal/domain"
)

// PushJobRepo persists broadcast tasks (§7.1).
type PushJobRepo struct{ s *Store }

// PushJobs returns a repo bound to this store.
func (s *Store) PushJobs() *PushJobRepo { return &PushJobRepo{s} }

// Create inserts a push job.
func (r *PushJobRepo) Create(ctx context.Context, j domain.PushJob) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO PushJob (job_id, pool_id, title, content, channel_id, channel_type, target_topic, required_entitlement, target_tier, status, scheduled_at, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		j.JobID, j.PoolID, j.Title, j.Content, nullStr(j.ChannelID), j.ChannelType, nullStr(j.TargetTopic),
		nullStr(j.RequiredEntitlement), nullStr(j.TargetTier), string(j.Status), tsPtr(j.ScheduledAt), j.CreatedBy, ts(j.CreatedAt))
	return err
}

const pushJobCols = `SELECT job_id, pool_id, title, content, channel_id, channel_type, target_topic, required_entitlement, target_tier, status, scheduled_at, created_by, created_at FROM PushJob`

func scanPushJob(row rowScanner) (*domain.PushJob, error) {
	var j domain.PushJob
	var status, created string
	var channelID, topic, ent, tier, scheduled sql.NullString
	if err := row.Scan(&j.JobID, &j.PoolID, &j.Title, &j.Content, &channelID, &j.ChannelType,
		&topic, &ent, &tier, &status, &scheduled, &j.CreatedBy, &created); err != nil {
		return nil, err
	}
	j.ChannelID, j.TargetTopic, j.RequiredEntitlement, j.TargetTier = strPtr(channelID), strPtr(topic), strPtr(ent), strPtr(tier)
	j.Status = domain.PushJobStatus(status)
	var err error
	if j.ScheduledAt, err = scanTS(scheduled); err != nil {
		return nil, err
	}
	if j.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *PushJobRepo) scanMany(rows *sql.Rows, qerr error) ([]domain.PushJob, error) {
	if qerr != nil {
		return nil, qerr
	}
	defer rows.Close()
	var out []domain.PushJob
	for rows.Next() {
		j, err := scanPushJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// Get loads a push job.
func (r *PushJobRepo) Get(ctx context.Context, jobID string) (*domain.PushJob, error) {
	j, err := scanPushJob(r.s.DB.QueryRowContext(ctx, r.s.Rebind(pushJobCols+` WHERE job_id = ?`), jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return j, err
}

// ListByPool returns a pool's push jobs, newest first.
func (r *PushJobRepo) ListByPool(ctx context.Context, poolID string, limit int) ([]domain.PushJob, error) {
	return r.scanMany(r.s.DB.QueryContext(ctx, r.s.Rebind(pushJobCols+
		` WHERE pool_id = ? ORDER BY created_at DESC LIMIT ?`), poolID, limit))
}

// ListScheduled returns due scheduled jobs (status='scheduled' and either no
// scheduled_at or scheduled_at <= now), oldest first, for the push worker to
// pick up (p12-4).
func (r *PushJobRepo) ListScheduled(ctx context.Context, now time.Time, limit int) ([]domain.PushJob, error) {
	return r.scanMany(r.s.DB.QueryContext(ctx, r.s.Rebind(pushJobCols+
		` WHERE status = ? AND (scheduled_at IS NULL OR scheduled_at <= ?) ORDER BY created_at LIMIT ?`),
		string(domain.PushScheduled), ts(now), limit))
}

// SetStatus transitions a job's status.
func (r *PushJobRepo) SetStatus(ctx context.Context, jobID string, status domain.PushJobStatus) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`UPDATE PushJob SET status = ? WHERE job_id = ?`),
		string(status), jobID)
	return err
}
