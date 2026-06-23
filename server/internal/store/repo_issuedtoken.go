package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"ouro-pass/server/internal/domain"
)

// IssuedTokenRepo persists the issued-token ledger (§4.1).
type IssuedTokenRepo struct{ s *Store }

// IssuedTokens returns a repo bound to this store.
func (s *Store) IssuedTokens() *IssuedTokenRepo { return &IssuedTokenRepo{s} }

// Create records an issued token.
func (r *IssuedTokenRepo) Create(ctx context.Context, q Querier, t domain.IssuedToken) error {
	if q == nil {
		q = r.s.DB
	}
	_, err := q.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO IssuedToken (jti, stake_credential_hash, kind, audience, kid, client_id, status, issued_at, expires_at, redeemed_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		t.JTI, t.StakeCredentialHash, string(t.Kind), t.Audience, t.KID, nullStr(t.ClientID),
		string(t.Status), ts(t.IssuedAt), ts(t.ExpiresAt), tsPtr(t.RedeemedAt), tsPtr(t.RevokedAt))
	return err
}

// Get loads a token by jti.
func (r *IssuedTokenRepo) Get(ctx context.Context, jti string) (*domain.IssuedToken, error) {
	var t domain.IssuedToken
	var kind, status, issued, expires string
	var clientID, redeemed, revoked sql.NullString
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT jti, stake_credential_hash, kind, audience, kid, client_id, status, issued_at, expires_at, redeemed_at, revoked_at
		FROM IssuedToken WHERE jti = ?`), jti).
		Scan(&t.JTI, &t.StakeCredentialHash, &kind, &t.Audience, &t.KID, &clientID, &status, &issued, &expires, &redeemed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Kind, t.Status, t.ClientID = domain.TokenKind(kind), domain.TokenStatus(status), strPtr(clientID)
	if t.IssuedAt, err = parseTS(issued); err != nil {
		return nil, err
	}
	if t.ExpiresAt, err = parseTS(expires); err != nil {
		return nil, err
	}
	if t.RedeemedAt, err = scanTS(redeemed); err != nil {
		return nil, err
	}
	if t.RevokedAt, err = scanTS(revoked); err != nil {
		return nil, err
	}
	return &t, nil
}

// Revoke marks a token revoked at the given time.
func (r *IssuedTokenRepo) Revoke(ctx context.Context, jti string, at time.Time) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(
		`UPDATE IssuedToken SET status = ?, revoked_at = ? WHERE jti = ?`),
		string(domain.TokenRevoked), ts(at), jti)
	return err
}

// RevokeByStakeCredential revokes every still-active token for a credential
// (admin member revoke, §9.8). Returns the number of rows affected.
func (r *IssuedTokenRepo) RevokeByStakeCredential(ctx context.Context, sch string, at time.Time) (int64, error) {
	res, err := r.s.DB.ExecContext(ctx, r.s.Rebind(
		`UPDATE IssuedToken SET status = ?, revoked_at = ? WHERE stake_credential_hash = ? AND status = ?`),
		string(domain.TokenRevoked), ts(at), sch, string(domain.TokenActive))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
