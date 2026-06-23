package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/poolops/issuer/internal/domain"
)

// MembershipRuleRepo persists eligibility rules (§3.1).
type MembershipRuleRepo struct{ s *Store }

// Rules returns a repo bound to this store.
func (s *Store) Rules() *MembershipRuleRepo { return &MembershipRuleRepo{s} }

// Upsert inserts or replaces a rule.
func (r *MembershipRuleRepo) Upsert(ctx context.Context, m domain.MembershipRule) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO MembershipRule (rule_id, name, rule_config, tier, entitlements, priority, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (rule_id) DO UPDATE SET
			name=excluded.name, rule_config=excluded.rule_config, tier=excluded.tier,
			entitlements=excluded.entitlements, priority=excluded.priority,
			status=excluded.status, updated_at=excluded.updated_at`),
		m.RuleID, m.Name, string(m.RuleConfig), m.Tier, encodeStrings(m.Entitlements),
		m.Priority, string(m.Status), ts(m.CreatedAt), ts(m.UpdatedAt))
	return err
}

// ListActive returns active rules ordered by priority (desc) then rule_id, so
// the rule engine sees a deterministic ordering (supports C10 determinism).
func (r *MembershipRuleRepo) ListActive(ctx context.Context) ([]domain.MembershipRule, error) {
	rows, err := r.s.DB.QueryContext(ctx, r.s.Rebind(`
		SELECT rule_id, name, rule_config, tier, entitlements, priority, status, created_at, updated_at
		FROM MembershipRule WHERE status = ? ORDER BY priority DESC, rule_id ASC`), string(domain.RuleActive))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MembershipRule
	for rows.Next() {
		var m domain.MembershipRule
		var cfg, ent, status, created, updated string
		if err := rows.Scan(&m.RuleID, &m.Name, &cfg, &m.Tier, &ent, &m.Priority, &status, &created, &updated); err != nil {
			return nil, err
		}
		m.RuleConfig = []byte(cfg)
		m.Status = domain.RuleStatus(status)
		if m.Entitlements, err = decodeStrings(ent); err != nil {
			return nil, err
		}
		if m.CreatedAt, err = parseTS(created); err != nil {
			return nil, err
		}
		if m.UpdatedAt, err = parseTS(updated); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// BlacklistRepo persists the sparse manual deny-list (§3.2).
type BlacklistRepo struct{ s *Store }

// Blacklist returns a repo bound to this store.
func (s *Store) Blacklist() *BlacklistRepo { return &BlacklistRepo{s} }

// Add inserts (or refreshes) a blacklist entry.
func (r *BlacklistRepo) Add(ctx context.Context, b domain.Blacklist) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO Blacklist (stake_credential_hash, reason, created_at) VALUES (?, ?, ?)
		ON CONFLICT (stake_credential_hash) DO UPDATE SET reason=excluded.reason`),
		b.StakeCredentialHash, nullStr(b.Reason), ts(b.CreatedAt))
	return err
}

// Has reports whether a stake credential is blacklisted.
func (r *BlacklistRepo) Has(ctx context.Context, stakeCredentialHash string) (bool, error) {
	var n int
	err := r.s.DB.QueryRowContext(ctx,
		r.s.Rebind(`SELECT COUNT(1) FROM Blacklist WHERE stake_credential_hash = ?`),
		stakeCredentialHash).Scan(&n)
	return n > 0, err
}

// SnapshotCacheRepo persists optional raw stake snapshots (§3.3).
type SnapshotCacheRepo struct{ s *Store }

// SnapshotCache returns a repo bound to this store.
func (s *Store) SnapshotCache() *SnapshotCacheRepo { return &SnapshotCacheRepo{s} }

// Upsert stores a raw snapshot row.
func (r *SnapshotCacheRepo) Upsert(ctx context.Context, c domain.StakeSnapshotCache) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO StakeSnapshotCache (stake_credential_hash, snapshot_epoch, delegated_pool_id, active_stake_lovelace, rewards_lovelace, source, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (stake_credential_hash) DO UPDATE SET
			snapshot_epoch=excluded.snapshot_epoch, delegated_pool_id=excluded.delegated_pool_id,
			active_stake_lovelace=excluded.active_stake_lovelace, rewards_lovelace=excluded.rewards_lovelace,
			source=excluded.source, fetched_at=excluded.fetched_at`),
		c.StakeCredentialHash, c.SnapshotEpoch, nullStr(c.DelegatedPoolID),
		nullStr(c.ActiveStakeLovelace), nullStr(c.RewardsLovelace), c.Source, ts(c.FetchedAt))
	return err
}

// Get loads a cached snapshot.
func (r *SnapshotCacheRepo) Get(ctx context.Context, stakeCredentialHash string) (*domain.StakeSnapshotCache, error) {
	var c domain.StakeSnapshotCache
	var pool, active, rewards sql.NullString
	var fetched string
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT stake_credential_hash, snapshot_epoch, delegated_pool_id, active_stake_lovelace, rewards_lovelace, source, fetched_at
		FROM StakeSnapshotCache WHERE stake_credential_hash = ?`), stakeCredentialHash).
		Scan(&c.StakeCredentialHash, &c.SnapshotEpoch, &pool, &active, &rewards, &c.Source, &fetched)
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
