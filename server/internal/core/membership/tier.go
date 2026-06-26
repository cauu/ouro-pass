package membership

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// TierRule is one entry of the issuer's first-party tier mapping (S0004 §2.6):
// "a credential in at least MinState with at least MinActiveStake gets Tier".
// Rules are evaluated in array order, first match wins.
type TierRule struct {
	Tier           string `json:"tier"`
	MinState       State  `json:"min_state"`        // "active" | "pending" (default: active)
	MinActiveStake string `json:"min_active_stake"` // decimal lovelace; "" = no minimum
}

// ValidateTierRules checks that rulesJSON is a well-formed ordered tier-rule
// array before it is persisted: each entry needs a tier, min_state must be
// empty/active/pending (none is meaningless as a floor), and min_active_stake
// must be empty or a base-10 integer. Empty/[] is valid (no tier opinion).
func ValidateTierRules(rulesJSON []byte) error {
	if len(rulesJSON) == 0 {
		return nil
	}
	var rules []TierRule
	if err := json.Unmarshal(rulesJSON, &rules); err != nil {
		return fmt.Errorf("tier_rules: invalid JSON: %w", err)
	}
	for i, r := range rules {
		if r.Tier == "" {
			return fmt.Errorf("tier_rules[%d]: tier is required", i)
		}
		switch r.MinState {
		case "", StateActive, StatePending:
		default:
			return fmt.Errorf("tier_rules[%d]: min_state must be active or pending, got %q", i, r.MinState)
		}
		if r.MinActiveStake != "" {
			if _, ok := new(big.Int).SetString(r.MinActiveStake, 10); !ok {
				return fmt.Errorf("tier_rules[%d]: min_active_stake %q is not an integer", i, r.MinActiveStake)
			}
		}
	}
	return nil
}

// stateRank orders membership for "at least" comparisons: none < pending < active.
func stateRank(s State) int {
	switch s {
	case StateActive:
		return 2
	case StatePending:
		return 1
	default:
		return 0
	}
}

// TierFor maps a credential's (state, active stake) to a first-party tier via the
// pool's ordered rules (first match wins). Returns "" when no rule matches or the
// rules are empty/invalid — the issuer simply has no tier opinion. Used ONLY by
// the issuer's own channels; external RPs derive their own policy from raw facts.
func TierFor(state State, activeStakeLovelace string, rulesJSON []byte) string {
	if len(rulesJSON) == 0 {
		return ""
	}
	var rules []TierRule
	if err := json.Unmarshal(rulesJSON, &rules); err != nil {
		return ""
	}
	have := new(big.Int)
	if activeStakeLovelace != "" {
		if _, ok := have.SetString(activeStakeLovelace, 10); !ok {
			have.SetInt64(0)
		}
	}
	for _, r := range rules {
		minState := r.MinState
		if minState == "" {
			minState = StateActive
		}
		if stateRank(state) < stateRank(minState) {
			continue
		}
		if r.MinActiveStake != "" {
			min := new(big.Int)
			if _, ok := min.SetString(r.MinActiveStake, 10); ok && have.Cmp(min) < 0 {
				continue
			}
		}
		return r.Tier
	}
	return ""
}
