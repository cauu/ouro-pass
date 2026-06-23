package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/poolops/issuer/internal/domain"
)

// ActivationCodeRepo persists one-time channel activation codes (§4.4).
type ActivationCodeRepo struct{ s *Store }

// ActivationCodes returns a repo bound to this store.
func (s *Store) ActivationCodes() *ActivationCodeRepo { return &ActivationCodeRepo{s} }

// Create inserts an activation code (code is a hash or the activation token jti).
func (r *ActivationCodeRepo) Create(ctx context.Context, a domain.ActivationCode) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO ActivationCode (code, stake_credential_hash, channel_type, status, expires_at, consumed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`),
		a.Code, a.StakeCredentialHash, a.ChannelType, string(a.Status), ts(a.ExpiresAt), tsPtr(a.ConsumedAt), ts(a.CreatedAt))
	return err
}

// Consume atomically marks an activation code consumed for the given channel,
// enforcing single-use and expiry (used by the Telegram bot, §9.7).
func (r *ActivationCodeRepo) Consume(ctx context.Context, code, channelType string, now time.Time) (*domain.ActivationCode, error) {
	var out *domain.ActivationCode
	err := r.s.WithTx(ctx, func(tx *sql.Tx) error {
		var a domain.ActivationCode
		var status, expires, created string
		var consumed sql.NullString
		err := tx.QueryRowContext(ctx, r.s.Rebind(`
			SELECT code, stake_credential_hash, channel_type, status, expires_at, consumed_at, created_at
			FROM ActivationCode WHERE code = ?`), code).
			Scan(&a.Code, &a.StakeCredentialHash, &a.ChannelType, &status, &expires, &consumed, &created)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if a.ChannelType != channelType {
			return domain.ErrPurpose
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
		if _, err := tx.ExecContext(ctx, r.s.Rebind(`UPDATE ActivationCode SET status = ?, consumed_at = ? WHERE code = ?`),
			string(domain.ActivationConsumed), ts(now), code); err != nil {
			return err
		}
		a.Status, a.ExpiresAt = domain.ActivationConsumed, exp
		if a.CreatedAt, err = parseTS(created); err != nil {
			return err
		}
		out = &a
		return nil
	})
	return out, err
}
