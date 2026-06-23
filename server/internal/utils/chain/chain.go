// Package chain is the Staking Index Adapter: a pluggable, read-only view of
// on-chain stake used by the rule engine (detailed §3.3). Implementations
// (node_lsq, db_sync, koios/blockfrost) are interchangeable behind Source; the
// service prefers self-hosted sources and never touches private keys. Real
// node/db-sync/HTTP integration is exercised under the `integration` build tag
// (decision D5); the parsing and selection logic here is unit-tested directly.
package chain

import (
	"context"
	"errors"
	"time"
)

// ErrNotImplemented marks a source whose real backend is not wired in this build.
var ErrNotImplemented = errors.New("chain: source not implemented")

// Snapshot is the raw on-chain stake view for one credential (not an
// eligibility conclusion — that is the rule engine's job). Lovelace amounts are
// decimal strings to preserve values beyond 2^53 (C4).
type Snapshot struct {
	StakeCredentialHash string
	Epoch               uint64
	DelegatedPoolID     string // "" if undelegated
	ActiveStakeLovelace string // "" if the source cannot provide per-credential stake
	RewardsLovelace     string
	Source              string
	FetchedAt           time.Time
}

// Source is a pluggable staking data provider.
type Source interface {
	// Snapshot returns the current stake view for a stake credential.
	Snapshot(ctx context.Context, stakeCredentialHash string) (*Snapshot, error)
	// Epoch returns the current Cardano epoch.
	Epoch(ctx context.Context) (uint64, error)
	// Name identifies the source (node_lsq | db_sync | koios | blockfrost | mock).
	Name() string
}

// MockSource is an in-memory Source for tests and local development; the rule
// engine and handlers depend only on the Source interface.
type MockSource struct {
	CurrentEpoch uint64
	Snapshots    map[string]*Snapshot
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

// Config selects and configures a Source.
type Config struct {
	Kind         string // node_lsq | db_sync | koios | blockfrost | mock
	KoiosBaseURL string
	APIKey       string
	NodeSocket   string
	CardanoCLI   string
	Network      string
}

// NewSource builds a Source from config. Unknown/unimplemented backends return
// a typed error so the caller can fail fast at startup.
func NewSource(cfg Config) (Source, error) {
	switch cfg.Kind {
	case "mock":
		return NewMockSource(0), nil
	case "koios":
		return NewKoiosSource(cfg.KoiosBaseURL, cfg.APIKey), nil
	case "node_lsq":
		return NewNodeLSQSource(cfg.CardanoCLI, cfg.NodeSocket, cfg.Network), nil
	case "db_sync":
		return &DBSyncSource{}, nil
	default:
		return nil, errors.New("chain: unknown source kind " + cfg.Kind)
	}
}
