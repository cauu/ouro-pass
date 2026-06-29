// Package membership derives a credential's pool-membership state from raw chain
// facts (S0004 §2.2). It is the thin replacement for the deleted rules engine's
// classification role: it answers "what is this credential's relationship to our
// pool right now" — active / pending / none — and leaves business policy
// (thresholds → entitlements) to relying parties (D1). It is pure: no I/O, no
// storage; callers supply the chain.Snapshot and the issuer's pool id.
package membership

import "ouro-pass/server/internal/utils/chain"

// State is a credential's membership relative to the issuer's pool.
type State string

const (
	// StateNone: not a member — no active stake with us and not entering.
	StateNone State = "none"
	// StatePending: registered and live-delegating to us, but active stake is not
	// yet effective (the ~2-epoch activation lag). An entering member.
	StatePending State = "pending"
	// StateActive: effective active stake currently counts for our pool. Includes
	// the ~2-epoch leaving tail — live delegation may have already moved away while
	// the active-stake snapshot still credits us.
	StateActive State = "active"
)

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	return s == StateNone || s == StatePending || s == StateActive
}

// DeriveState classifies a credential relative to poolID from raw chain facts
// (S0004 §2.2). active dominates pending dominates none. State is membership
// only — it does not depend on the stake amount; amount → tier is a separate,
// first-party concern (PoolConfig.tier_rules).
//
//	active  iff active stake counts for our pool (ActiveStakePoolID == poolID)
//	pending iff registered & live delegation to us, not yet active
//	none    otherwise
func DeriveState(snap *chain.Snapshot, poolID string) State {
	if snap == nil || poolID == "" {
		return StateNone
	}
	// Normalize all pool ids to canonical bech32 so a hex-configured pool compares equal
	// to the bech32 form Koios returns (S0014 p4-1); without this a hex pool never matched.
	want := chain.CanonicalPoolID(poolID)
	// `active`: the effective active-stake snapshot credits our pool. This is the
	// authoritative membership signal and naturally carries the leaving tail.
	if chain.CanonicalPoolID(snap.ActiveStakePoolID) == want {
		return StateActive
	}
	// `pending`: entered (registered + live delegation to us) but not yet active.
	if snap.AccountStatus == "registered" && chain.CanonicalPoolID(snap.DelegatedPoolID) == want {
		return StatePending
	}
	return StateNone
}
