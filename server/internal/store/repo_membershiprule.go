package store

import (
	"context"

	"ouro-pass/server/internal/domain"
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
