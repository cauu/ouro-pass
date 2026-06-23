package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"ouro-pass/server/internal/domain"
)

// AuthNonceRepo persists one-time signing nonces (§4.5).
type AuthNonceRepo struct{ s *Store }

// AuthNonces returns a repo bound to this store.
func (s *Store) AuthNonces() *AuthNonceRepo { return &AuthNonceRepo{s} }

// Create inserts a fresh nonce.
func (r *AuthNonceRepo) Create(ctx context.Context, n domain.AuthNonce) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO AuthNonce (nonce, purpose, bound_key_hash, expires_at, consumed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`),
		n.Nonce, string(n.Purpose), nullStr(n.BoundKeyHash), ts(n.ExpiresAt), tsPtr(n.ConsumedAt), ts(n.CreatedAt))
	return err
}

// DeleteExpired removes nonces whose validity window has passed (consumed or
// not — once expired they are useless). Returns the number of rows deleted.
// Replay protection is unaffected: an expired row would be rejected anyway, and
// only-still-valid rows are retained.
func (r *AuthNonceRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`DELETE FROM AuthNonce WHERE expires_at < ?`), ts(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Consume atomically marks a nonce used, enforcing single-use, expiry, and
// purpose. Returns ErrNotFound/ErrConsumed/ErrExpired/ErrPurpose as applicable.
func (r *AuthNonceRepo) Consume(ctx context.Context, nonce string, purpose domain.NoncePurpose, now time.Time) (*domain.AuthNonce, error) {
	var out *domain.AuthNonce
	err := r.s.WithTx(ctx, func(tx *sql.Tx) error {
		var n domain.AuthNonce
		var p, expires, created string
		var boundKey, consumed sql.NullString
		err := tx.QueryRowContext(ctx, r.s.Rebind(`
			SELECT nonce, purpose, bound_key_hash, expires_at, consumed_at, created_at FROM AuthNonce WHERE nonce = ?`), nonce).
			Scan(&n.Nonce, &p, &boundKey, &expires, &consumed, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if consumed.Valid && consumed.String != "" {
			return domain.ErrConsumed
		}
		exp, err := parseTS(expires)
		if err != nil {
			return err
		}
		if now.After(exp) {
			return domain.ErrExpired
		}
		if domain.NoncePurpose(p) != purpose {
			return domain.ErrPurpose
		}
		if _, err := tx.ExecContext(ctx, r.s.Rebind(`UPDATE AuthNonce SET consumed_at = ? WHERE nonce = ?`), ts(now), nonce); err != nil {
			return err
		}
		n.Purpose, n.BoundKeyHash, n.ExpiresAt = purpose, strPtr(boundKey), exp
		if n.CreatedAt, err = parseTS(created); err != nil {
			return err
		}
		out = &n
		return nil
	})
	return out, err
}
