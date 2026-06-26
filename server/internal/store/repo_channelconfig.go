package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

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
	return r.scanOne(r.s.DB.QueryRowContext(ctx, r.s.Rebind(channelCols+
		` WHERE channel_type = ? AND status = 'active' ORDER BY updated_at DESC LIMIT 1`), channelType))
}

// Create inserts a new channel instance (S0005 p1-2). It rejects a duplicate
// instance name within a (pool_id, channel_type) with domain.ErrConflict before
// the unique index would, so the API can return a clean 409.
func (r *ChannelConfigRepo) Create(ctx context.Context, c domain.ChannelConfig) error {
	return r.s.WithTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, r.s.Rebind(
			`SELECT COUNT(1) FROM ChannelConfig WHERE pool_id = ? AND channel_type = ? AND name = ?`),
			c.PoolID, c.ChannelType, c.Name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return domain.ErrConflict
		}
		_, err := tx.ExecContext(ctx, r.s.Rebind(`
			INSERT INTO ChannelConfig (channel_id, pool_id, channel_type, name, config, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
			c.ChannelID, c.PoolID, c.ChannelType, c.Name, string(c.Config), c.Status, ts(c.CreatedAt), ts(c.UpdatedAt))
		return err
	})
}

// Get returns a single instance by its stable channel_id.
func (r *ChannelConfigRepo) Get(ctx context.Context, channelID string) (*domain.ChannelConfig, error) {
	return r.scanOne(r.s.DB.QueryRowContext(ctx, r.s.Rebind(channelCols+` WHERE channel_id = ?`), channelID))
}

// List returns all of a pool's channel instances (any status) for admin
// management, ordered by type then name for a stable UI.
func (r *ChannelConfigRepo) List(ctx context.Context, poolID string) ([]domain.ChannelConfig, error) {
	return r.scanMany(r.s.DB.QueryContext(ctx, r.s.Rebind(channelCols+
		` WHERE pool_id = ? ORDER BY channel_type, name`), poolID))
}

// ListActive returns a pool's active instances of one channel type — the
// supervisor's reconciliation set (S0005 p2-1).
func (r *ChannelConfigRepo) ListActive(ctx context.Context, poolID, channelType string) ([]domain.ChannelConfig, error) {
	return r.scanMany(r.s.DB.QueryContext(ctx, r.s.Rebind(channelCols+
		` WHERE pool_id = ? AND channel_type = ? AND status = 'active' ORDER BY name`), poolID, channelType))
}

// SetStatus enables/disables an instance by id (active|disabled).
func (r *ChannelConfigRepo) SetStatus(ctx context.Context, channelID, status string, now time.Time) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(
		`UPDATE ChannelConfig SET status = ?, updated_at = ? WHERE channel_id = ?`), status, ts(now), channelID)
	return err
}

// Delete removes an instance by id.
func (r *ChannelConfigRepo) Delete(ctx context.Context, channelID string) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`DELETE FROM ChannelConfig WHERE channel_id = ?`), channelID)
	return err
}

const channelCols = `SELECT channel_id, pool_id, channel_type, name, config, status, created_at, updated_at FROM ChannelConfig`

func (r *ChannelConfigRepo) scanOne(row rowScanner) (*domain.ChannelConfig, error) {
	c, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return c, err
}

func (r *ChannelConfigRepo) scanMany(rows *sql.Rows, qerr error) ([]domain.ChannelConfig, error) {
	if qerr != nil {
		return nil, qerr
	}
	defer rows.Close()
	var out []domain.ChannelConfig
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func scanChannel(row rowScanner) (*domain.ChannelConfig, error) {
	var c domain.ChannelConfig
	var config, created, updated string
	if err := row.Scan(&c.ChannelID, &c.PoolID, &c.ChannelType, &c.Name, &config, &c.Status, &created, &updated); err != nil {
		return nil, err
	}
	c.Config = []byte(config)
	var err error
	if c.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	if c.UpdatedAt, err = parseTS(updated); err != nil {
		return nil, err
	}
	return &c, nil
}
