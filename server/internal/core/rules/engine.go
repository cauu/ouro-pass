// Package rules is the eligibility engine — the most business-specific code in
// the service. Evaluate is a pure function (decision C10): given a stake
// snapshot, the active rules, and the current epoch it returns a deterministic
// Decision with no IO, no clock, and no side effects. Inputs (snapshot, rules,
// epoch) are injected by the caller; multiple rules are sorted before matching
// so the same inputs always produce the same output.
package rules

import (
	"encoding/json"
	"math/big"
	"sort"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/chain"
)

// Input is the eligibility evaluation input for one identity.
type Input struct {
	PoolID              string // the issuer's pool (eligibility target)
	DelegatedPoolID     string // the credential's current delegation
	ActiveStakeLovelace string // decimal string; "" if the source can't provide it
	EpochsDelegated     int    // -1 if unknown
	Epoch               uint64
}

// InputFromSnapshot maps a chain snapshot to an evaluation input (pure).
func InputFromSnapshot(poolID string, s *chain.Snapshot) Input {
	return Input{
		PoolID:              poolID,
		DelegatedPoolID:     s.DelegatedPoolID,
		ActiveStakeLovelace: s.ActiveStakeLovelace,
		EpochsDelegated:     -1, // single snapshot carries no delegation age
		Epoch:               s.Epoch,
	}
}

// ruleConfig is the parsed MembershipRule.rule_config (§3.1). Unknown keys are
// ignored so rules can evolve without code changes.
type ruleConfig struct {
	RequiredStatus         string `json:"required_status"`
	MinActiveStakeLovelace string `json:"min_active_stake_lovelace"`
	MinActiveEpochs        int    `json:"min_active_epochs"`
	GraceEpochs            int    `json:"grace_epochs"`
}

// Decision is the eligibility outcome.
type Decision struct {
	Eligible     bool
	Tier         string
	Entitlements []string
	MatchedRule  string
	Reason       string
}

// Evaluate selects the highest-priority rule the input satisfies. It is pure
// and deterministic: rules are sorted by (priority desc, rule_id asc) before
// matching, and the first match wins. No clock or IO is consulted.
func Evaluate(in Input, ruleset []domain.MembershipRule, epoch uint64) Decision {
	sorted := make([]domain.MembershipRule, len(ruleset))
	copy(sorted, ruleset)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Priority != sorted[j].Priority {
			return sorted[i].Priority > sorted[j].Priority
		}
		return sorted[i].RuleID < sorted[j].RuleID
	})

	for _, r := range sorted {
		if r.Status != domain.RuleActive {
			continue
		}
		var cfg ruleConfig
		if len(r.RuleConfig) > 0 {
			if err := json.Unmarshal(r.RuleConfig, &cfg); err != nil {
				continue // malformed rule config never matches
			}
		}
		if satisfies(in, cfg) {
			return Decision{
				Eligible:     true,
				Tier:         r.Tier,
				Entitlements: append([]string(nil), r.Entitlements...),
				MatchedRule:  r.RuleID,
				Reason:       "matched " + r.RuleID,
			}
		}
	}
	return Decision{Eligible: false, Reason: "no matching rule"}
}

// satisfies reports whether the input meets a rule's conditions (pure).
func satisfies(in Input, cfg ruleConfig) bool {
	// Must currently delegate to the issuer's pool.
	if in.DelegatedPoolID == "" || in.DelegatedPoolID != in.PoolID {
		return false
	}
	// Minimum active stake (exact big-int comparison; C4).
	if cfg.MinActiveStakeLovelace != "" {
		min, ok := new(big.Int).SetString(cfg.MinActiveStakeLovelace, 10)
		if !ok {
			return false
		}
		have, ok := new(big.Int).SetString(in.ActiveStakeLovelace, 10)
		if !ok {
			return false // required a minimum but stake is unknown/unparseable
		}
		if have.Cmp(min) < 0 {
			return false
		}
	}
	// Minimum delegation age, net of grace, when the source provides it.
	if cfg.MinActiveEpochs > 0 && in.EpochsDelegated >= 0 {
		effective := max(cfg.MinActiveEpochs-cfg.GraceEpochs, 0)
		if in.EpochsDelegated < effective {
			return false
		}
	}
	return true
}
