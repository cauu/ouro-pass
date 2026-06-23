package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/poolops/issuer/internal/domain"
)

// ---- PushJob (§7.1) ----

// PushJobRepo persists broadcast tasks.
type PushJobRepo struct{ s *Store }

// PushJobs returns a repo bound to this store.
func (s *Store) PushJobs() *PushJobRepo { return &PushJobRepo{s} }

// Create inserts a push job.
func (r *PushJobRepo) Create(ctx context.Context, j domain.PushJob) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO PushJob (job_id, pool_id, title, content, channel_type, target_topic, required_entitlement, target_tier, status, scheduled_at, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		j.JobID, j.PoolID, j.Title, j.Content, j.ChannelType, nullStr(j.TargetTopic),
		nullStr(j.RequiredEntitlement), nullStr(j.TargetTier), string(j.Status), tsPtr(j.ScheduledAt), j.CreatedBy, ts(j.CreatedAt))
	return err
}

// Get loads a push job.
func (r *PushJobRepo) Get(ctx context.Context, jobID string) (*domain.PushJob, error) {
	var j domain.PushJob
	var status, created string
	var topic, ent, tier, scheduled sql.NullString
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT job_id, pool_id, title, content, channel_type, target_topic, required_entitlement, target_tier, status, scheduled_at, created_by, created_at
		FROM PushJob WHERE job_id = ?`), jobID).
		Scan(&j.JobID, &j.PoolID, &j.Title, &j.Content, &j.ChannelType, &topic, &ent, &tier, &status, &scheduled, &j.CreatedBy, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j.TargetTopic, j.RequiredEntitlement, j.TargetTier = strPtr(topic), strPtr(ent), strPtr(tier)
	j.Status = domain.PushJobStatus(status)
	if j.ScheduledAt, err = scanTS(scheduled); err != nil {
		return nil, err
	}
	if j.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	return &j, nil
}

// SetStatus transitions a job's status.
func (r *PushJobRepo) SetStatus(ctx context.Context, jobID string, status domain.PushJobStatus) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`UPDATE PushJob SET status = ? WHERE job_id = ?`),
		string(status), jobID)
	return err
}

// ---- DeliveryLog (§7.2) ----

// DeliveryLogRepo persists per-recipient delivery records.
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

// ---- AdminUser (§8.1) ----

// AdminUserRepo persists admin users.
type AdminUserRepo struct{ s *Store }

// AdminUsers returns a repo bound to this store.
func (s *Store) AdminUsers() *AdminUserRepo { return &AdminUserRepo{s} }

// Upsert inserts or updates an admin keyed by owner_key_hash.
func (r *AdminUserRepo) Upsert(ctx context.Context, u domain.AdminUser) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO AdminUser (admin_id, pool_id, owner_key_hash, role, last_login_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (owner_key_hash) DO UPDATE SET role=excluded.role`),
		u.AdminID, u.PoolID, u.OwnerKeyHash, string(u.Role), tsPtr(u.LastLoginAt), ts(u.CreatedAt))
	return err
}

// GetByOwnerKeyHash loads an admin by their login key hash.
func (r *AdminUserRepo) GetByOwnerKeyHash(ctx context.Context, ownerKeyHash string) (*domain.AdminUser, error) {
	var u domain.AdminUser
	var role, created string
	var lastLogin sql.NullString
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT admin_id, pool_id, owner_key_hash, role, last_login_at, created_at FROM AdminUser WHERE owner_key_hash = ?`), ownerKeyHash).
		Scan(&u.AdminID, &u.PoolID, &u.OwnerKeyHash, &role, &lastLogin, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role = domain.AdminRole(role)
	if u.LastLoginAt, err = scanTS(lastLogin); err != nil {
		return nil, err
	}
	if u.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByID loads an admin by id (session → role lookup).
func (r *AdminUserRepo) GetByID(ctx context.Context, adminID string) (*domain.AdminUser, error) {
	var u domain.AdminUser
	var role, created string
	var lastLogin sql.NullString
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT admin_id, pool_id, owner_key_hash, role, last_login_at, created_at FROM AdminUser WHERE admin_id = ?`), adminID).
		Scan(&u.AdminID, &u.PoolID, &u.OwnerKeyHash, &role, &lastLogin, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role = domain.AdminRole(role)
	if u.LastLoginAt, err = scanTS(lastLogin); err != nil {
		return nil, err
	}
	if u.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	return &u, nil
}

// TouchLogin stamps last_login_at.
func (r *AdminUserRepo) TouchLogin(ctx context.Context, adminID string, at time.Time) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`UPDATE AdminUser SET last_login_at = ? WHERE admin_id = ?`), ts(at), adminID)
	return err
}

// ---- AdminSession (§8.2) ----

// AdminSessionRepo persists admin sessions (token stored hashed).
type AdminSessionRepo struct{ s *Store }

// AdminSessions returns a repo bound to this store.
func (s *Store) AdminSessions() *AdminSessionRepo { return &AdminSessionRepo{s} }

// Create inserts a session.
func (r *AdminSessionRepo) Create(ctx context.Context, sess domain.AdminSession) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO AdminSession (session_token, admin_id, expires_at, ip, created_at) VALUES (?, ?, ?, ?, ?)`),
		sess.SessionToken, sess.AdminID, ts(sess.ExpiresAt), nullStr(sess.IP), ts(sess.CreatedAt))
	return err
}

// GetValid loads a non-expired session by its hashed token.
func (r *AdminSessionRepo) GetValid(ctx context.Context, tokenHash string, now time.Time) (*domain.AdminSession, error) {
	var sess domain.AdminSession
	var expires, created string
	var ip sql.NullString
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT session_token, admin_id, expires_at, ip, created_at FROM AdminSession WHERE session_token = ?`), tokenHash).
		Scan(&sess.SessionToken, &sess.AdminID, &expires, &ip, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if sess.ExpiresAt, err = parseTS(expires); err != nil {
		return nil, err
	}
	if now.After(sess.ExpiresAt) {
		return nil, domain.ErrExpired
	}
	sess.IP = strPtr(ip)
	if sess.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	return &sess, nil
}

// Delete removes a session (logout).
func (r *AdminSessionRepo) Delete(ctx context.Context, tokenHash string) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`DELETE FROM AdminSession WHERE session_token = ?`), tokenHash)
	return err
}

// ---- AuditLog (§8.3) ----

// AuditLogRepo persists the audit trail.
type AuditLogRepo struct{ s *Store }

// Audit returns a repo bound to this store.
func (s *Store) Audit() *AuditLogRepo { return &AuditLogRepo{s} }

// Append writes an audit entry.
func (r *AuditLogRepo) Append(ctx context.Context, a domain.AuditLog) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO AuditLog (audit_id, actor, action, target, before_hash, after_hash, ip, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		a.AuditID, a.Actor, a.Action, a.Target, nullStr(a.BeforeHash), nullStr(a.AfterHash), nullStr(a.IP), ts(a.CreatedAt))
	return err
}

// Recent returns the most recent audit entries, newest first.
func (r *AuditLogRepo) Recent(ctx context.Context, limit int) ([]domain.AuditLog, error) {
	rows, err := r.s.DB.QueryContext(ctx, r.s.Rebind(`
		SELECT audit_id, actor, action, target, before_hash, after_hash, ip, created_at
		FROM AuditLog ORDER BY created_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AuditLog
	for rows.Next() {
		var a domain.AuditLog
		var created string
		var before, after, ip sql.NullString
		if err := rows.Scan(&a.AuditID, &a.Actor, &a.Action, &a.Target, &before, &after, &ip, &created); err != nil {
			return nil, err
		}
		a.BeforeHash, a.AfterHash, a.IP = strPtr(before), strPtr(after), strPtr(ip)
		if a.CreatedAt, err = parseTS(created); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
