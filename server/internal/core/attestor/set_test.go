package attestor

import (
	"context"
	"encoding/json"
	"testing"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/chain"
)

func TestSet_EvaluateAggregate(t *testing.T) {
	const epoch = 500
	mock := chain.NewMockSource(epoch)
	// active in A with 5_000_000; pending in B; nothing else.
	mock.Put(&chain.Snapshot{
		StakeCredentialHash: "s", Epoch: epoch, AccountStatus: "registered",
		DelegatedPoolID: poolA, ActiveStakePoolID: poolA, ActiveStakeLovelace: "5000000",
		EpochsDelegated: 3, Source: "mock",
	})
	set := NewSet([]Attestor{
		buildPool(t, "a", poolA, mock),
		buildPool(t, "b", poolB, mock),
	})

	res, err := set.Evaluate(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Held {
		t.Fatal("active in A → Held")
	}
	if len(res.Attestations) != 2 {
		t.Fatalf("attestations: %d", len(res.Attestations))
	}
	if res.Facts[FactAnyActive] != "true" || res.Facts[FactAnyHeld] != "true" {
		t.Fatalf("aggregate flags: %v", res.Facts)
	}
	if res.Facts[FactTotalActiveStake] != "5000000" {
		t.Fatalf("total_active_stake: %q", res.Facts[FactTotalActiveStake])
	}
	if res.Facts[PoolFactKey(poolA, "state")] != "active" || res.Facts[PoolFactKey(poolB, "state")] != "none" {
		t.Fatalf("namespaced facts: %v", res.Facts)
	}
}

func TestSet_EvaluateNotHeld(t *testing.T) {
	mock := chain.NewMockSource(500) // unknown subject → zero-stake snapshot
	set := NewSet([]Attestor{buildPool(t, "a", poolA, mock), buildPool(t, "b", poolB, mock)})
	res, err := set.Evaluate(context.Background(), "stranger")
	if err != nil {
		t.Fatal(err)
	}
	if res.Held || res.Facts[FactAnyActive] != "false" || res.Facts[FactAnyHeld] != "false" {
		t.Fatalf("stranger should not be held: %+v", res.Facts)
	}
	if res.Facts[FactTotalActiveStake] != "0" {
		t.Fatalf("total should be 0: %q", res.Facts[FactTotalActiveStake])
	}
}

// TestSet_Empty is the cold-start case (S0006 p4-1): zero configured attestors →
// nobody is held, so the thin gate denies everyone until an attestor is configured.
func TestSet_Empty(t *testing.T) {
	res, err := NewSet(nil).Evaluate(context.Background(), "anyone")
	if err != nil {
		t.Fatal(err)
	}
	if res.Held || res.Facts[FactAnyActive] != "false" || res.Facts[FactTotalActiveStake] != "0" {
		t.Fatalf("empty set must hold nobody: %+v", res.Facts)
	}
}

func TestBuildSet(t *testing.T) {
	mock := chain.NewMockSource(1)
	mkCfg := func(id, pool string) domain.AttestorConfig {
		params, _ := json.Marshal(PoolStakeParams{PoolID: pool, Network: "mainnet"})
		return domain.AttestorConfig{AttestorID: id, Kind: KindPoolStake, Params: params, Status: domain.AttestorActive}
	}
	set, err := BuildSet(
		[]domain.AttestorConfig{mkCfg("a", poolA), mkCfg("b", poolB)},
		DefaultRegistry(), srcForMock(mock),
	)
	if err != nil || len(set.Attestors()) != 2 {
		t.Fatalf("build set: %v len=%d", err, len(set.Attestors()))
	}
	// A bad config (unknown kind) fails the whole build.
	if _, err := BuildSet([]domain.AttestorConfig{{AttestorID: "x", Kind: "nft"}}, DefaultRegistry(), srcForMock(mock)); err == nil {
		t.Fatal("unknown kind must fail BuildSet")
	}
}
