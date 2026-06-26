// Package tier is the issuer's first-party tier mapping (S0006 §2.4): an ordered
// list of rules, each pairing a tier name with a boolean expression over the
// aggregate named facts an attestor set produces (e.g. any_active,
// total_active_stake, pool:<id>.state). First matching rule wins; no match → no
// tier (the subject is not a first-party subscriber). The expression language is a
// small, declarative boolean DSL (all/any/not + {fact,op,value}) — deliberately
// NOT Turing-complete (no loops, no arithmetic beyond comparison, no user code) —
// so rules stay analyzable. Used ONLY by the issuer's own channels; external
// relying parties derive their own policy from the raw token facts.
package tier

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Rule pairs a tier name with the condition a subject must satisfy.
type Rule struct {
	Tier string    `json:"tier"`
	When Condition `json:"when"`
}

// Condition is a boolean expression node over named facts. Exactly one of the
// forms is set: All (AND), Any (OR), Not (negation), or a leaf {Fact,Op,Value}.
// An empty condition matches everything (a catch-all tier).
type Condition struct {
	All   []Condition `json:"all,omitempty"`
	Any   []Condition `json:"any,omitempty"`
	Not   *Condition  `json:"not,omitempty"`
	Fact  string      `json:"fact,omitempty"`
	Op    string      `json:"op,omitempty"`
	Value string      `json:"value,omitempty"`
}

// Eval returns the first matching rule's tier, or "" when no rule matches or the
// rules are empty/invalid (the issuer simply has no tier opinion).
func Eval(rulesJSON []byte, facts map[string]string) string {
	var rules []Rule
	if len(rulesJSON) == 0 || json.Unmarshal(rulesJSON, &rules) != nil {
		return ""
	}
	for _, r := range rules {
		if r.When.eval(facts) {
			return r.Tier
		}
	}
	return ""
}

// Validate checks that rulesJSON is a well-formed ordered rule array before it is
// persisted. Empty/[] is valid (no tier opinion).
func Validate(rulesJSON []byte) error {
	if len(rulesJSON) == 0 {
		return nil
	}
	var rules []Rule
	if err := json.Unmarshal(rulesJSON, &rules); err != nil {
		return fmt.Errorf("tier_rules: invalid JSON: %w", err)
	}
	for i, r := range rules {
		if r.Tier == "" {
			return fmt.Errorf("tier_rules[%d]: tier is required", i)
		}
		if err := r.When.validate(); err != nil {
			return fmt.Errorf("tier_rules[%d].when: %w", i, err)
		}
	}
	return nil
}

func (c Condition) eval(facts map[string]string) bool {
	switch {
	case len(c.All) > 0:
		for _, sub := range c.All {
			if !sub.eval(facts) {
				return false
			}
		}
		return true
	case len(c.Any) > 0:
		for _, sub := range c.Any {
			if sub.eval(facts) {
				return true
			}
		}
		return false
	case c.Not != nil:
		return !c.Not.eval(facts)
	case c.Fact != "":
		return compare(facts[c.Fact], c.Op, c.Value)
	default:
		return true // empty condition = catch-all
	}
}

func (c Condition) validate() error {
	forms := 0
	for _, set := range []bool{len(c.All) > 0, len(c.Any) > 0, c.Not != nil, c.Fact != ""} {
		if set {
			forms++
		}
	}
	if forms > 1 {
		return fmt.Errorf("condition must be exactly one of all/any/not/leaf")
	}
	if (c.Op != "" || c.Value != "") && c.Fact == "" {
		return fmt.Errorf("op/value require a fact")
	}
	for _, sub := range c.All {
		if err := sub.validate(); err != nil {
			return err
		}
	}
	for _, sub := range c.Any {
		if err := sub.validate(); err != nil {
			return err
		}
	}
	if c.Not != nil {
		if err := c.Not.validate(); err != nil {
			return err
		}
	}
	if c.Fact != "" {
		switch c.Op {
		case "==", "!=", ">=", ">", "<=", "<":
		default:
			return fmt.Errorf("invalid op %q (want == != >= > <= <)", c.Op)
		}
	}
	return nil
}

// compare evaluates `have <op> want`. == / != are string equality; the ordering
// ops parse both sides as big.Int (a missing/empty numeric fact is treated as 0,
// an unparseable comparand yields false).
func compare(have, op, want string) bool {
	switch op {
	case "==":
		return have == want
	case "!=":
		return have != want
	case ">=", ">", "<=", "<":
		h, ok1 := new(big.Int).SetString(nz(have), 10)
		w, ok2 := new(big.Int).SetString(want, 10)
		if !ok1 || !ok2 {
			return false
		}
		c := h.Cmp(w)
		switch op {
		case ">=":
			return c >= 0
		case ">":
			return c > 0
		case "<=":
			return c <= 0
		default: // "<"
			return c < 0
		}
	}
	return false
}

func nz(s string) string {
	if s == "" {
		return "0"
	}
	return s
}
