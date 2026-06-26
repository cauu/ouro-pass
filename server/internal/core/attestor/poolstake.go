package attestor

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"ouro-pass/server/internal/core/membership"
	"ouro-pass/server/internal/utils/chain"
)

// PoolStakeParams configures a pool_stake attestor: which pool, on which network
// (S0006 D2/D4). Ticker/Name are display-only carry-overs from the pool's identity
// (the former PoolConfig fields).
type PoolStakeParams struct {
	PoolID  string `json:"pool_id"`
	Network string `json:"network"`
	Ticker  string `json:"ticker,omitempty"`
	Name    string `json:"name,omitempty"`
}

// PoolStakeAttestor attests a subject's membership in one pool, wrapping the S0004
// staking derivation (membership.DeriveState + facts). Held = state != none, i.e.
// active or pending (entering) both satisfy the credential.
type PoolStakeAttestor struct {
	id     string
	params PoolStakeParams
	src    chain.Source
}

// BuildPoolStake is the Registry Builder for KindPoolStake.
func BuildPoolStake(id string, raw json.RawMessage, srcFor SourceFor) (Attestor, error) {
	var p PoolStakeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("pool_stake[%s] params: %w", id, err)
	}
	if p.PoolID == "" {
		return nil, fmt.Errorf("pool_stake[%s]: pool_id is required", id)
	}
	src, err := srcFor(p.Network)
	if err != nil {
		return nil, fmt.Errorf("pool_stake[%s]: source for network %q: %w", id, p.Network, err)
	}
	return &PoolStakeAttestor{id: id, params: p, src: src}, nil
}

// Kind returns "pool_stake".
func (a *PoolStakeAttestor) Kind() string { return KindPoolStake }

// ID returns the attestor's stable id.
func (a *PoolStakeAttestor) ID() string { return a.id }

// Attest snapshots the subject's stake and derives its membership in this pool,
// producing the token claim entry and the per-pool aggregate facts.
func (a *PoolStakeAttestor) Attest(ctx context.Context, subject string) (*Attestation, error) {
	snap, err := a.src.Snapshot(ctx, subject)
	if err != nil {
		return nil, err
	}
	state := membership.DeriveState(snap, a.params.PoolID)

	claim := map[string]any{
		"kind":    KindPoolStake,
		"pool":    a.params.PoolID,
		"network": a.params.Network,
		"state":   string(state),
	}
	facts := map[string]string{
		PoolFactKey(a.params.PoolID, "state"): string(state),
	}
	if snap != nil {
		if snap.ActiveStakeLovelace != "" {
			claim["active_stake_lovelace"] = snap.ActiveStakeLovelace
			facts[PoolFactKey(a.params.PoolID, "active_stake_lovelace")] = snap.ActiveStakeLovelace
		}
		if snap.EpochsDelegated > 0 {
			claim["epochs_active"] = snap.EpochsDelegated
			facts[PoolFactKey(a.params.PoolID, "epochs_active")] = strconv.Itoa(snap.EpochsDelegated)
		}
		if ms := memberSince(a.params.Network, snap); !ms.IsZero() {
			claim["member_since"] = ms.UTC().Format(time.RFC3339)
		}
	}
	return &Attestation{
		Kind:  KindPoolStake,
		ID:    a.id,
		Held:  state != membership.StateNone,
		Claim: claim,
		Facts: facts,
	}, nil
}

// PoolFactKey namespaces a per-pool fact for the aggregate tier DSL (S0006 §2.4):
// "pool:<id>.<name>", e.g. "pool:pool1….active_stake_lovelace".
func PoolFactKey(poolID, name string) string { return "pool:" + poolID + "." + name }

// memberSince stamps when the credential's current active run began: the start of
// epoch (snapshotEpoch - epochsActive + 1). Zero for non-active / unknown network.
// (Carried over from oauth.memberSince, now per-pool.)
func memberSince(network string, snap *chain.Snapshot) time.Time {
	if snap.EpochsDelegated <= 0 {
		return time.Time{}
	}
	start := snap.Epoch - uint64(snap.EpochsDelegated) + 1
	t, ok := chain.EpochStart(network, start)
	if !ok {
		return time.Time{}
	}
	return t
}
