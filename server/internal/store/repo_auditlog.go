package store

import (
	"context"
	"database/sql"

	"ouro-pass/server/internal/domain"
)

// AuditLogRepo persists the audit trail (§8.3).
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
