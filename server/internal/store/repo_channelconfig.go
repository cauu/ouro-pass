package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/poolops/issuer/internal/domain"
)

// ChannelConfigRepo persists channel instances (§6.1).
type ChannelConfigRepo struct{ s *Store }

// Channels returns a repo bound to this store.
func (s *Store) Channels() *ChannelConfigRepo { return &ChannelConfigRepo{s} }

// Upsert inserts or replaces a channel config (secret subfields pre-encrypted).
func (r *ChannelConfigRepo) Upsert(ctx context.Context, c domain.ChannelConfig) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO ChannelConfig (channel_id, pool_id, channel_type, config, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (channel_id) DO UPDATE SET
			config=excluded.config, status=excluded.status, updated_at=excluded.updated_at`),
		c.ChannelID, c.PoolID, c.ChannelType, string(c.Config), c.Status, ts(c.CreatedAt), ts(c.UpdatedAt))
	return err
}

// GetByType returns the active channel config for a channel type, if any.
func (r *ChannelConfigRepo) GetByType(ctx context.Context, channelType string) (*domain.ChannelConfig, error) {
	var c domain.ChannelConfig
	var config, created, updated string
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT channel_id, pool_id, channel_type, config, status, created_at, updated_at
		FROM ChannelConfig WHERE channel_type = ? AND status = 'active' LIMIT 1`), channelType).
		Scan(&c.ChannelID, &c.PoolID, &c.ChannelType, &config, &c.Status, &created, &updated)
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
