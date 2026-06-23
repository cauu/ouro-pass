package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/poolops/issuer/internal/domain"
)

// ---- IssuedToken ledger (§4.1) ----

// IssuedTokenRepo persists the issued-token ledger.
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

// ---- RefreshGrant (§4.2) ----

// RefreshGrantRepo persists refresh grants and their rotation chain.
type RefreshGrantRepo struct{ s *Store }

// RefreshGrants returns a repo bound to this store.
func (s *Store) RefreshGrants() *RefreshGrantRepo { return &RefreshGrantRepo{s} }

// Create inserts a refresh grant (id is a hash of the plaintext).
func (r *RefreshGrantRepo) Create(ctx context.Context, q Querier, g domain.RefreshGrant) error {
	if q == nil {
		q = r.s.DB
	}
	_, err := q.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO RefreshGrant (refresh_grant_id, stake_credential_hash, audience, client_type, bound_device_pubkey, client_id, status, rotated_from, created_at, expires_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		g.RefreshGrantID, g.StakeCredentialHash, g.Audience, string(g.ClientType), g.BoundDevicePubkey,
		nullStr(g.ClientID), string(g.Status), nullStr(g.RotatedFrom), ts(g.CreatedAt), tsPtr(g.ExpiresAt), tsPtr(g.LastUsedAt))
	return err
}

// Get loads a grant by id.
func (r *RefreshGrantRepo) Get(ctx context.Context, id string) (*domain.RefreshGrant, error) {
	var g domain.RefreshGrant
	var clientType, status, created string
	var clientID, rotatedFrom, expires, lastUsed sql.NullString
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT refresh_grant_id, stake_credential_hash, audience, client_type, bound_device_pubkey, client_id, status, rotated_from, created_at, expires_at, last_used_at
		FROM RefreshGrant WHERE refresh_grant_id = ?`), id).
		Scan(&g.RefreshGrantID, &g.StakeCredentialHash, &g.Audience, &clientType, &g.BoundDevicePubkey, &clientID, &status, &rotatedFrom, &created, &expires, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.ClientType, g.Status = domain.ClientType(clientType), domain.GrantStatus(status)
	g.ClientID, g.RotatedFrom = strPtr(clientID), strPtr(rotatedFrom)
	if g.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	if g.ExpiresAt, err = scanTS(expires); err != nil {
		return nil, err
	}
	if g.LastUsedAt, err = scanTS(lastUsed); err != nil {
		return nil, err
	}
	return &g, nil
}

// SetStatus transitions a grant's status (used by rotation / revocation).
func (r *RefreshGrantRepo) SetStatus(ctx context.Context, q Querier, id string, status domain.GrantStatus) error {
	if q == nil {
		q = r.s.DB
	}
	_, err := q.ExecContext(ctx, r.s.Rebind(`UPDATE RefreshGrant SET status = ? WHERE refresh_grant_id = ?`),
		string(status), id)
	return err
}

// RevokeChain revokes a grant and every descendant reachable via rotated_from,
// the theft-response action when a rotated grant is replayed (detailed §9.4).
func (r *RefreshGrantRepo) RevokeChain(ctx context.Context, startID string) error {
	return r.s.WithTx(ctx, func(tx *sql.Tx) error {
		ids := []string{startID}
		for len(ids) > 0 {
			cur := ids[0]
			ids = ids[1:]
			if _, err := tx.ExecContext(ctx, r.s.Rebind(`UPDATE RefreshGrant SET status = ? WHERE refresh_grant_id = ?`),
				string(domain.GrantRevoked), cur); err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, r.s.Rebind(`SELECT refresh_grant_id FROM RefreshGrant WHERE rotated_from = ?`), cur)
			if err != nil {
				return err
			}
			for rows.Next() {
				var child string
				if err := rows.Scan(&child); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, child)
			}
			rows.Close()
		}
		return nil
	})
}

// ---- AuthNonce (§4.5) ----

// AuthNonceRepo persists one-time signing nonces.
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

// ---- AuthorizationCode (§4.3) ----

// AuthCodeRepo persists one-time OAuth authorization codes.
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

// ---- ActivationCode (§4.4) ----

// ActivationCodeRepo persists one-time channel activation codes.
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
