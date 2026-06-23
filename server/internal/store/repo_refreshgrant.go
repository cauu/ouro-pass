package store

import (
	"context"
	"database/sql"
	"errors"

	"ouro-pass/server/internal/domain"
)

// RefreshGrantRepo persists refresh grants and their rotation chain (§4.2).
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

// RotateIfActive atomically transitions an active grant to rotated, returning
// true only for the caller that won the transition. Under concurrency only one
// of several refreshes of the same grant can rotate it (compare-and-swap); the
// losers see false and must be rejected (detailed §9.4, p12-2).
func (r *RefreshGrantRepo) RotateIfActive(ctx context.Context, q Querier, id string) (bool, error) {
	if q == nil {
		q = r.s.DB
	}
	res, err := q.ExecContext(ctx, r.s.Rebind(
		`UPDATE RefreshGrant SET status = ? WHERE refresh_grant_id = ? AND status = ?`),
		string(domain.GrantRotated), id, string(domain.GrantActive))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RevokeByStakeCredential revokes every non-revoked grant for a credential
// (admin member revoke, §9.8). Returns the number of rows affected.
func (r *RefreshGrantRepo) RevokeByStakeCredential(ctx context.Context, sch string) (int64, error) {
	res, err := r.s.DB.ExecContext(ctx, r.s.Rebind(
		`UPDATE RefreshGrant SET status = ? WHERE stake_credential_hash = ? AND status != ?`),
		string(domain.GrantRevoked), sch, string(domain.GrantRevoked))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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
			// A mid-iteration read error must abort the chain revoke, not be
			// silently treated as a complete walk (under-revocation, p12-9).
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}
		return nil
	})
}
