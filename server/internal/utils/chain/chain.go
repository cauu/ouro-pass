// Package chain is the Staking Index Adapter: a pluggable, read-only view of
// on-chain stake used by the rule engine (detailed §3.3). Koios is the single
// chain origin (S0015): KoiosSource fetches per-network from the public Koios API
// and never touches private keys. MockSource is an in-memory test double injected
// directly (never selected via env). Real HTTP integration is exercised under the
// `integration` build tag (decision D5); the parsing logic here is unit-tested.
package chain

import (
	"context"
	"errors"
	"time"
)

// ErrNotImplemented marks an optional capability a source does not provide
// (e.g. delegator enumeration via DelegatorLister).
var ErrNotImplemented = errors.New("chain: capability not implemented")

// Snapshot is the raw on-chain stake view for one credential (raw facts, not an
// eligibility/membership conclusion — that is derived against the pool by
// DeriveState, S0004 §2.2). Lovelace amounts are decimal strings to preserve
// values beyond 2^53 (C4).
//
// Two distinct delegation signals (S0004 §2.4) must not be conflated:
//   - DelegatedPoolID is the *live* delegation (account_info) — drives `pending`.
//   - ActiveStakePoolID is the pool whose *effective active stake* snapshot
//     currently counts this credential (account_stake_history latest entry) —
//     drives `active`, and lags live delegation by ~2 epochs (the leaving tail).
type Snapshot struct {
	StakeCredentialHash string
	Epoch               uint64
	DelegatedPoolID     string // live delegation (account_info); "" if undelegated/unregistered
	ActiveStakePoolID   string // pool of the latest active-stake snapshot; "" if no active stake
	ActiveStakeLovelace string // exact effective active stake (account_stake_history); "" if none
	RewardsLovelace     string
	EpochsDelegated     int    // trailing consecutive epochs active with ActiveStakePoolID; -1 if the source can't tell
	AccountStatus       string // registered | not_registered | ""(unknown)
	Source              string
	FetchedAt           time.Time
}

// Source is a pluggable staking data provider.
type Source interface {
	// Snapshot returns the current stake view for a stake credential.
	Snapshot(ctx context.Context, stakeCredentialHash string) (*Snapshot, error)
	// Epoch returns the current Cardano epoch.
	Epoch(ctx context.Context) (uint64, error)
	// Name identifies the source (koios | mock).
	Name() string
}

// DelegatorLister is an OPTIONAL capability (S0004 §2.7, track C): enumerate a
// pool's full delegator set, one page at a time. It is decoupled from the hot
// authorization path — a cold, read-only admin roster query — so it is a separate
// interface, not part of Source: sources that cannot provide it simply don't
// implement it, and callers type-assert. Returns stake credential hashes (hex).
type DelegatorLister interface {
	Delegators(ctx context.Context, poolID string, page int) ([]string, error)
}

// MockSource is an in-memory Source for tests and local development; the rule
// engine and handlers depend only on the Source interface.
type MockSource struct {
	CurrentEpoch     uint64
	Snapshots        map[string]*Snapshot
	DelegatorsByPool map[string][]string // poolID → stake credential hashes (optional)
}

// NewMockSource builds an empty mock at the given epoch.
func NewMockSource(epoch uint64) *MockSource {
	return &MockSource{CurrentEpoch: epoch, Snapshots: map[string]*Snapshot{}}
}

// Put registers a snapshot for a credential.
func (m *MockSource) Put(s *Snapshot) { m.Snapshots[s.StakeCredentialHash] = s }

// Snapshot returns the registered snapshot or a zero-stake snapshot if absent.
func (m *MockSource) Snapshot(_ context.Context, h string) (*Snapshot, error) {
	if s, ok := m.Snapshots[h]; ok {
		return s, nil
	}
	return &Snapshot{StakeCredentialHash: h, Epoch: m.CurrentEpoch, Source: "mock"}, nil
}

// Epoch returns the mock epoch.
func (m *MockSource) Epoch(context.Context) (uint64, error) { return m.CurrentEpoch, nil }

// Name returns "mock".
func (m *MockSource) Name() string { return "mock" }

// Delegators returns the configured delegator page for a pool (the mock ignores
// paging beyond page 0, returning the full set on page 0). Implements the
// optional DelegatorLister capability for tests.
func (m *MockSource) Delegators(_ context.Context, poolID string, page int) ([]string, error) {
	if page > 0 {
		return nil, nil
	}
	return m.DelegatorsByPool[poolID], nil
}

// Source construction is direct (S0015): the issuer builds NewKoiosSource per
// network; tests inject NewMockSource. There is no kind-dispatch factory.
