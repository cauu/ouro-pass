package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/poolops/issuer/internal/domain"
)

// SubscriptionRepo persists channel subscriptions (§6.2).
type SubscriptionRepo struct{ s *Store }

// Subscriptions returns a repo bound to this store.
func (s *Store) Subscriptions() *SubscriptionRepo { return &SubscriptionRepo{s} }

// Upsert inserts or replaces a session keyed by (pool, channel_type, channel_user_id).
func (r *SubscriptionRepo) Upsert(ctx context.Context, x domain.SubscriptionSession) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO SubscriptionSession (session_id, pool_id, stake_credential_hash, channel_type, channel_user_id, channel_account_id, status, tier, topics, entitlements, created_at, last_verified_at, expires_at, cancelled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (pool_id, channel_type, channel_user_id) DO UPDATE SET
			stake_credential_hash=excluded.stake_credential_hash, status=excluded.status, tier=excluded.tier,
			topics=excluded.topics, entitlements=excluded.entitlements, last_verified_at=excluded.last_verified_at,
			expires_at=excluded.expires_at, cancelled_at=excluded.cancelled_at`),
		x.SessionID, x.PoolID, x.StakeCredentialHash, x.ChannelType, x.ChannelUserID, nullStr(x.ChannelAccountID),
		string(x.Status), x.Tier, encodeStrings(x.Topics), encodeStrings(x.Entitlements),
		ts(x.CreatedAt), ts(x.LastVerifiedAt), ts(x.ExpiresAt), tsPtr(x.CancelledAt))
	return err
}

// GetByChannelUser loads a session by its channel-user unique key (bot lookups).
func (r *SubscriptionRepo) GetByChannelUser(ctx context.Context, poolID, channelType, channelUserID string) (*domain.SubscriptionSession, error) {
	return r.scanOne(r.s.DB.QueryRowContext(ctx, r.s.Rebind(subscriptionCols+
		` WHERE pool_id = ? AND channel_type = ? AND channel_user_id = ?`), poolID, channelType, channelUserID))
}

// ListActiveByChannel returns all active sessions for a pool's channel (the
// push-scheduler candidate set; tier/topic/entitlement filtering is applied by
// the scheduler in code over the JSON array columns).
func (r *SubscriptionRepo) ListActiveByChannel(ctx context.Context, poolID, channelType string) ([]domain.SubscriptionSession, error) {
	rows, err := r.s.DB.QueryContext(ctx, r.s.Rebind(subscriptionCols+
		` WHERE pool_id = ? AND channel_type = ? AND status = ?`), poolID, channelType, string(domain.SubActive))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SubscriptionSession
	for rows.Next() {
		x, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// ListActive returns all active sessions for a pool across channels (the
// reconciliation candidate set).
func (r *SubscriptionRepo) ListActive(ctx context.Context, poolID string) ([]domain.SubscriptionSession, error) {
	rows, err := r.s.DB.QueryContext(ctx, r.s.Rebind(subscriptionCols+
		` WHERE pool_id = ? AND status = ?`), poolID, string(domain.SubActive))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SubscriptionSession
	for rows.Next() {
		x, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// SetStatus transitions a session's status (downgrade/cancel/expire).
func (r *SubscriptionRepo) SetStatus(ctx context.Context, sessionID string, status domain.SubscriptionStatus) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`UPDATE SubscriptionSession SET status = ? WHERE session_id = ?`),
		string(status), sessionID)
	return err
}

const subscriptionCols = `SELECT session_id, pool_id, stake_credential_hash, channel_type, channel_user_id, channel_account_id, status, tier, topics, entitlements, created_at, last_verified_at, expires_at, cancelled_at FROM SubscriptionSession`

func (r *SubscriptionRepo) scanOne(row rowScanner) (*domain.SubscriptionSession, error) {
	x, err := scanSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return x, err
}

func scanSubscription(row rowScanner) (*domain.SubscriptionSession, error) {
	var x domain.SubscriptionSession
	var status, tier, topics, ents, created, lastVer, expires string
	var acct, cancelled sql.NullString
	if err := row.Scan(&x.SessionID, &x.PoolID, &x.StakeCredentialHash, &x.ChannelType, &x.ChannelUserID,
		&acct, &status, &tier, &topics, &ents, &created, &lastVer, &expires, &cancelled); err != nil {
		return nil, err
	}
	x.ChannelAccountID, x.Status, x.Tier = strPtr(acct), domain.SubscriptionStatus(status), tier
	var err error
	if x.Topics, err = decodeStrings(topics); err != nil {
		return nil, err
	}
	if x.Entitlements, err = decodeStrings(ents); err != nil {
		return nil, err
	}
	if x.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	if x.LastVerifiedAt, err = parseTS(lastVer); err != nil {
		return nil, err
	}
	if x.ExpiresAt, err = parseTS(expires); err != nil {
		return nil, err
	}
	if x.CancelledAt, err = scanTS(cancelled); err != nil {
		return nil, err
	}
	return &x, nil
}
