package attestor

import (
	"context"
	"encoding/json"
	"testing"

	"ouro-pass/server/internal/utils/chain"
)

const (
	poolA = "pool1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	poolB = "pool1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// srcForMock returns a SourceFor that hands the same mock to every network.
func srcForMock(m chain.Source) SourceFor {
	return func(string) (chain.Source, error) { return m, nil }
}

func buildPool(t *testing.T, id, pool string, m chain.Source) Attestor {
	t.Helper()
	raw, _ := json.Marshal(PoolStakeParams{PoolID: pool, Network: "mainnet"})
	a, err := BuildPoolStake(id, raw, srcForMock(m))
	if err != nil {
		t.Fatalf("build %s: %v", id, err)
	}
	return a
}

// TestPoolStake_MultiPool: two pool_stake attestors over the same subjects, each
// independently deriving membership in its own pool (the multi-pool seam, p1-1).
func TestPoolStake_MultiPool(t *testing.T) {
	const epoch = 500
	mock := chain.NewMockSource(epoch)
	// activeA: effective active stake credits poolA.
	mock.Put(&chain.Snapshot{
		StakeCredentialHash: "activeA", Epoch: epoch, AccountStatus: "registered",
		DelegatedPoolID: poolA, ActiveStakePoolID: poolA, ActiveStakeLovelace: "5000000",
		EpochsDelegated: 3, Source: "mock",
	})
	// pendingB: registered + live-delegating to poolB, not yet active anywhere.
	mock.Put(&chain.Snapshot{
		StakeCredentialHash: "pendingB", Epoch: epoch, AccountStatus: "registered",
		DelegatedPoolID: poolB, Source: "mock",
	})

	atA := buildPool(t, "att-a", poolA, mock)
	atB := buildPool(t, "att-b", poolB, mock)
	ctx := context.Background()

	// activeA: held+active in A, not-held in B.
	if a, _ := atA.Attest(ctx, "activeA"); !a.Held || a.Claim["state"] != "active" {
		t.Fatalf("activeA via A: held=%v claim=%v", a.Held, a.Claim)
	}
	if a, _ := atB.Attest(ctx, "activeA"); a.Held || a.Claim["state"] != "none" {
		t.Fatalf("activeA via B should be none/not-held: held=%v claim=%v", a.Held, a.Claim)
	}
	// pendingB: held+pending in B, not-held in A.
	if a, _ := atB.Attest(ctx, "pendingB"); !a.Held || a.Claim["state"] != "pending" {
		t.Fatalf("pendingB via B: held=%v claim=%v", a.Held, a.Claim)
	}
	if a, _ := atA.Attest(ctx, "pendingB"); a.Held {
		t.Fatalf("pendingB via A should be not-held: %v", a.Claim)
	}
}

// TestPoolStake_ClaimAndFacts: the active claim carries the per-pool facts and the
// aggregate fact keys are namespaced by pool id.
func TestPoolStake_ClaimAndFacts(t *testing.T) {
	const epoch = 500
	mock := chain.NewMockSource(epoch)
	mock.Put(&chain.Snapshot{
		StakeCredentialHash: "s", Epoch: epoch, AccountStatus: "registered",
		DelegatedPoolID: poolA, ActiveStakePoolID: poolA, ActiveStakeLovelace: "5000000",
		EpochsDelegated: 3, Source: "mock",
	})
	a, err := buildPool(t, "att-a", poolA, mock).Attest(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != KindPoolStake || a.ID != "att-a" {
		t.Fatalf("kind/id: %q %q", a.Kind, a.ID)
	}
	if a.Claim["active_stake_lovelace"] != "5000000" || a.Claim["epochs_active"] != 3 {
		t.Fatalf("claim facts: %v", a.Claim)
	}
	if _, ok := a.Claim["member_since"]; !ok {
		t.Fatalf("active claim should carry member_since: %v", a.Claim)
	}
	if a.Facts[PoolFactKey(poolA, "state")] != "active" ||
		a.Facts[PoolFactKey(poolA, "active_stake_lovelace")] != "5000000" ||
		a.Facts[PoolFactKey(poolA, "epochs_active")] != "3" {
		t.Fatalf("namespaced facts: %v", a.Facts)
	}
}

// TestRegistry: pool_stake builds via the default registry; unknown kinds and
// missing pool_id fail loudly (so a misconfigured credential is never silent).
func TestRegistry(t *testing.T) {
	reg := DefaultRegistry()
	mock := chain.NewMockSource(1)

	raw, _ := json.Marshal(PoolStakeParams{PoolID: poolA, Network: "mainnet"})
	if _, err := reg.Build(KindPoolStake, "att-a", raw, srcForMock(mock)); err != nil {
		t.Fatalf("build pool_stake: %v", err)
	}
	if _, err := reg.Build(KindNFT, "att-n", json.RawMessage(`{}`), srcForMock(mock)); err == nil {
		t.Fatal("nft kind must be unregistered this cycle")
	}
	if _, err := reg.Build(KindPoolStake, "bad", json.RawMessage(`{"network":"mainnet"}`), srcForMock(mock)); err == nil {
		t.Fatal("missing pool_id must error")
	}
}
