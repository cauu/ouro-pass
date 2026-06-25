package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"ouro-pass/server/internal/domain"
)

// PoolConfigRepo persists the served pool configuration (§2.1).
type PoolConfigRepo struct{ s *Store }

// PoolConfig returns a repo bound to this store.
func (s *Store) PoolConfig() *PoolConfigRepo { return &PoolConfigRepo{s} }

// Upsert inserts or replaces the pool configuration.
func (r *PoolConfigRepo) Upsert(ctx context.Context, p domain.PoolConfig) error {
	tierRules := string(p.TierRules)
	if tierRules == "" {
		tierRules = "[]"
	}
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO PoolConfig (pool_id, ticker, name, metadata_url, network, tier_rules, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (pool_id) DO UPDATE SET
			ticker=excluded.ticker, name=excluded.name, metadata_url=excluded.metadata_url,
			network=excluded.network, tier_rules=excluded.tier_rules, updated_at=excluded.updated_at`),
		p.PoolID, p.Ticker, nullStr(p.Name), nullStr(p.MetadataURL), p.Network, tierRules, ts(p.CreatedAt), ts(p.UpdatedAt))
	return err
}

// Get loads the pool configuration by id.
func (r *PoolConfigRepo) Get(ctx context.Context, poolID string) (*domain.PoolConfig, error) {
	var p domain.PoolConfig
	var name, metaURL sql.NullString
	var tierRules, created, updated string
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT pool_id, ticker, name, metadata_url, network, tier_rules, created_at, updated_at
		FROM PoolConfig WHERE pool_id = ?`), poolID).
		Scan(&p.PoolID, &p.Ticker, &name, &metaURL, &p.Network, &tierRules, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Name, p.MetadataURL = strPtr(name), strPtr(metaURL)
	p.TierRules = json.RawMessage(tierRules)
	if p.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	if p.UpdatedAt, err = parseTS(updated); err != nil {
		return nil, err
	}
	return &p, nil
}
