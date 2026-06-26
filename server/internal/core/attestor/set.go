package attestor

import (
	"context"
	"math/big"
	"strings"

	"ouro-pass/server/internal/domain"
)

// Aggregate fact names derived across the whole set (S0006 §2.4). Per-attestor
// facts are namespaced (e.g. "pool:<id>.state"); these are the cross-attestor
// rollups the tier DSL can reference directly.
const (
	FactAnyHeld          = "any_held"           // "true" if any credential is held (active|pending)
	FactAnyActive        = "any_active"         // "true" if any credential is in active state
	FactTotalActiveStake = "total_active_stake" // decimal sum of per-credential active_stake_lovelace
)

// Set is a resolved collection of attestors evaluated together for a subject.
type Set struct{ attestors []Attestor }

// NewSet wraps a fixed list of attestors (in evaluation order).
func NewSet(a []Attestor) *Set { return &Set{attestors: a} }

// Attestors returns the underlying attestors (read-only use).
func (s *Set) Attestors() []Attestor { return s.attestors }

// BuildSet constructs a Set from attestor configs via the registry, resolving a
// chain source per config through srcFor. A misconfigured/unknown-kind config
// fails the whole build (the caller surfaces it) rather than silently dropping a
// configured credential.
func BuildSet(cfgs []domain.AttestorConfig, reg *Registry, srcFor SourceFor) (*Set, error) {
	as := make([]Attestor, 0, len(cfgs))
	for _, c := range cfgs {
		a, err := reg.Build(c.Kind, c.AttestorID, c.Params, srcFor)
		if err != nil {
			return nil, err
		}
		as = append(as, a)
	}
	return NewSet(as), nil
}

// Result is the aggregate of evaluating a Set against a subject.
type Result struct {
	Attestations []*Attestation    // per-attestor, in set order
	Held         bool              // ANY held → the thin issuer gate passes (S0006 D7)
	Facts        map[string]string // per-attestor namespaced facts + derived aggregate facts
}

// Evaluate runs every attestor, unions their namespaced facts, and derives the
// aggregate facts used by tier evaluation. A single attestor error fails the whole
// evaluation; the caller decides fail-closed vs soft fail-open (S0004 D8).
func (s *Set) Evaluate(ctx context.Context, subject string) (*Result, error) {
	res := &Result{Facts: map[string]string{}}
	for _, a := range s.attestors {
		att, err := a.Attest(ctx, subject)
		if err != nil {
			return nil, err
		}
		res.Attestations = append(res.Attestations, att)
		if att.Held {
			res.Held = true
		}
		for k, v := range att.Facts {
			res.Facts[k] = v
		}
	}
	res.Facts[FactAnyHeld] = boolStr(res.Held)
	res.Facts[FactAnyActive] = boolStr(anyStateActive(res.Facts))
	res.Facts[FactTotalActiveStake] = sumActiveStake(res.Facts)
	return res, nil
}

// anyStateActive reports whether any per-attestor ".state" fact is "active".
func anyStateActive(facts map[string]string) bool {
	for k, v := range facts {
		if strings.HasSuffix(k, ".state") && v == "active" {
			return true
		}
	}
	return false
}

// sumActiveStake totals every per-attestor ".active_stake_lovelace" fact as a
// big.Int and returns it as a decimal string.
func sumActiveStake(facts map[string]string) string {
	total := new(big.Int)
	for k, v := range facts {
		if !strings.HasSuffix(k, ".active_stake_lovelace") || v == "" {
			continue
		}
		if n, ok := new(big.Int).SetString(v, 10); ok {
			total.Add(total, n)
		}
	}
	return total.String()
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
