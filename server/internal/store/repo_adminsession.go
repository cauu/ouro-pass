package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/poolops/issuer/internal/domain"
)

// AdminSessionRepo persists admin sessions (token stored hashed) (§8.2).
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
