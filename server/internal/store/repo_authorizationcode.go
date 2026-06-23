package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/poolops/issuer/internal/domain"
)

// AuthCodeRepo persists one-time OAuth authorization codes (§4.3).
type AuthCodeRepo struct{ s *Store }

// AuthCodes returns a repo bound to this store.
func (s *Store) AuthCodes() *AuthCodeRepo { return &AuthCodeRepo{s} }

// Create inserts an authorization code (code is a hash).
func (r *AuthCodeRepo) Create(ctx context.Context, c domain.AuthorizationCode) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO AuthorizationCode (code, client_id, stake_credential_hash, aud, scope, redirect_uri, code_challenge, expires_at, consumed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		c.Code, c.ClientID, c.StakeCredentialHash, c.Aud, encodeStrings(c.Scope), c.RedirectURI,
		nullStr(c.CodeChallenge), ts(c.ExpiresAt), tsPtr(c.ConsumedAt), ts(c.CreatedAt))
	return err
}

// Consume atomically redeems an authorization code once, enforcing expiry.
func (r *AuthCodeRepo) Consume(ctx context.Context, code string, now time.Time) (*domain.AuthorizationCode, error) {
	var out *domain.AuthorizationCode
	err := r.s.WithTx(ctx, func(tx *sql.Tx) error {
		var c domain.AuthorizationCode
		var scope, expires, created string
		var challenge, consumed sql.NullString
		err := tx.QueryRowContext(ctx, r.s.Rebind(`
			SELECT code, client_id, stake_credential_hash, aud, scope, redirect_uri, code_challenge, expires_at, consumed_at, created_at
			FROM AuthorizationCode WHERE code = ?`), code).
			Scan(&c.Code, &c.ClientID, &c.StakeCredentialHash, &c.Aud, &scope, &c.RedirectURI, &challenge, &expires, &consumed, &created)
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
		if _, err := tx.ExecContext(ctx, r.s.Rebind(`UPDATE AuthorizationCode SET consumed_at = ? WHERE code = ?`), ts(now), code); err != nil {
			return err
		}
		c.CodeChallenge = strPtr(challenge)
		c.ExpiresAt = exp
		if c.Scope, err = decodeStrings(scope); err != nil {
			return err
		}
		if c.CreatedAt, err = parseTS(created); err != nil {
			return err
		}
		out = &c
		return nil
	})
	return out, err
}
