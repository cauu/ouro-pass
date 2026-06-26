package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// IssuerConfigRepo persists issuer-global settings (S0006 §2.2). Today it holds
// the first-party tier_rules — moved out of PoolConfig because tiering now
// evaluates over the AGGREGATE of all attestors, not a single pool. It is a
// singleton row.
type IssuerConfigRepo struct{ s *Store }

// Issuer returns a repo bound to this store.
func (s *Store) Issuer() *IssuerConfigRepo { return &IssuerConfigRepo{s} }

// issuerConfigID is the fixed primary key of the singleton IssuerConfig row.
const issuerConfigID = "default"

// GetTierRules returns the global first-party tier-rule JSON, or "[]" when unset
// (no tier opinion). Never returns ErrNotFound — absence is an empty ruleset.
func (r *IssuerConfigRepo) GetTierRules(ctx context.Context) (json.RawMessage, error) {
	var rules string
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(
		`SELECT tier_rules FROM IssuerConfig WHERE id = ?`), issuerConfigID).Scan(&rules)
	if errors.Is(err, sql.ErrNoRows) || rules == "" {
		return json.RawMessage("[]"), nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(rules), nil
}

// SetTierRules upserts the global tier rules (caller validates the DSL first).
func (r *IssuerConfigRepo) SetTierRules(ctx context.Context, rules json.RawMessage, now time.Time) error {
	s := string(rules)
	if s == "" {
		s = "[]"
	}
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO IssuerConfig (id, tier_rules, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET tier_rules=excluded.tier_rules, updated_at=excluded.updated_at`),
		issuerConfigID, s, ts(now))
	return err
}
