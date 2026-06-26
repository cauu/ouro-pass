package store

import (
	"context"
	"database/sql"
	"errors"

	"ouro-pass/server/internal/domain"
)

// ChannelConfigRepo persists channel instances (§6.1).
type ChannelConfigRepo struct{ s *Store }

// Channels returns a repo bound to this store.
func (s *Store) Channels() *ChannelConfigRepo { return &ChannelConfigRepo{s} }

// Upsert inserts or replaces a channel config by channel_id (secret subfields
// pre-encrypted). The instance name is set on insert and updatable on conflict.
func (r *ChannelConfigRepo) Upsert(ctx context.Context, c domain.ChannelConfig) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO ChannelConfig (channel_id, pool_id, channel_type, name, config, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (channel_id) DO UPDATE SET
			name=excluded.name, config=excluded.config, status=excluded.status, updated_at=excluded.updated_at`),
		c.ChannelID, c.PoolID, c.ChannelType, c.Name, string(c.Config), c.Status, ts(c.CreatedAt), ts(c.UpdatedAt))
	return err
}

// ReplaceByType makes c the single config for its (pool_id, channel_type): it
// atomically removes any previous rows of that type and inserts c. This enforces
// one instance per channel type — the current design (multi-instance is future
// work) — and self-heals duplicate rows from earlier new-row-per-save behaviour.
func (r *ChannelConfigRepo) ReplaceByType(ctx context.Context, c domain.ChannelConfig) error {
	return r.s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, r.s.Rebind(
			`DELETE FROM ChannelConfig WHERE pool_id = ? AND channel_type = ?`), c.PoolID, c.ChannelType); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, r.s.Rebind(`
			INSERT INTO ChannelConfig (channel_id, pool_id, channel_type, name, config, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
			c.ChannelID, c.PoolID, c.ChannelType, c.Name, string(c.Config), c.Status, ts(c.CreatedAt), ts(c.UpdatedAt))
		return err
	})
}

// GetByType returns the active channel config for a channel type, if any. Ordered
// by recency so re-configuration deterministically wins even if stale duplicate
// rows linger.
func (r *ChannelConfigRepo) GetByType(ctx context.Context, channelType string) (*domain.ChannelConfig, error) {
	var c domain.ChannelConfig
	var config, created, updated string
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT channel_id, pool_id, channel_type, name, config, status, created_at, updated_at
		FROM ChannelConfig WHERE channel_type = ? AND status = 'active' ORDER BY updated_at DESC LIMIT 1`), channelType).
		Scan(&c.ChannelID, &c.PoolID, &c.ChannelType, &c.Name, &config, &c.Status, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Config = []byte(config)
	if c.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	if c.UpdatedAt, err = parseTS(updated); err != nil {
		return nil, err
	}
	return &c, nil
}
