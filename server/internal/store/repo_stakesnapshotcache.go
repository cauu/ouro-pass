package store

import (
	"context"
	"database/sql"
	"errors"

	"ouro-pass/server/internal/domain"
)

// SnapshotCacheRepo persists the active-membership snapshot cache (S0004 §2.3).
type SnapshotCacheRepo struct{ s *Store }

// SnapshotCache returns a repo bound to this store.
func (s *Store) SnapshotCache() *SnapshotCacheRepo { return &SnapshotCacheRepo{s} }

// Upsert stores an active-membership snapshot row.
func (r *SnapshotCacheRepo) Upsert(ctx context.Context, c domain.StakeSnapshotCache) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO StakeSnapshotCache (stake_credential_hash, snapshot_epoch, delegated_pool_id, active_stake_lovelace, rewards_lovelace, epochs_active, source, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (stake_credential_hash) DO UPDATE SET
			snapshot_epoch=excluded.snapshot_epoch, delegated_pool_id=excluded.delegated_pool_id,
			active_stake_lovelace=excluded.active_stake_lovelace, rewards_lovelace=excluded.rewards_lovelace,
			epochs_active=excluded.epochs_active, source=excluded.source, fetched_at=excluded.fetched_at`),
		c.StakeCredentialHash, c.SnapshotEpoch, nullStr(c.DelegatedPoolID),
		nullStr(c.ActiveStakeLovelace), nullStr(c.RewardsLovelace), c.EpochsActive, c.Source, ts(c.FetchedAt))
	return err
}

// Get loads a cached snapshot.
func (r *SnapshotCacheRepo) Get(ctx context.Context, stakeCredentialHash string) (*domain.StakeSnapshotCache, error) {
	var c domain.StakeSnapshotCache
	var pool, active, rewards sql.NullString
	var fetched string
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT stake_credential_hash, snapshot_epoch, delegated_pool_id, active_stake_lovelace, rewards_lovelace, epochs_active, source, fetched_at
		FROM StakeSnapshotCache WHERE stake_credential_hash = ?`), stakeCredentialHash).
		Scan(&c.StakeCredentialHash, &c.SnapshotEpoch, &pool, &active, &rewards, &c.EpochsActive, &c.Source, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.DelegatedPoolID, c.ActiveStakeLovelace, c.RewardsLovelace = strPtr(pool), strPtr(active), strPtr(rewards)
	if c.FetchedAt, err = parseTS(fetched); err != nil {
		return nil, err
	}
	return &c, nil
}

// Delete removes a credential's cached row (called on bail: active → pending/none,
// S0004 §2.3). Idempotent — deleting an absent row is not an error.
func (r *SnapshotCacheRepo) Delete(ctx context.Context, stakeCredentialHash string) error {
	_, err := r.s.DB.ExecContext(ctx,
		r.s.Rebind(`DELETE FROM StakeSnapshotCache WHERE stake_credential_hash = ?`), stakeCredentialHash)
	return err
}
