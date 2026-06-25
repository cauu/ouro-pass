package membership

import (
	"testing"

	"ouro-pass/server/internal/utils/chain"
)

func TestDeriveState(t *testing.T) {
	const us = "pool1us"
	cases := []struct {
		name string
		snap *chain.Snapshot
		want State
	}{
		{
			name: "entering: registered, live-delegating to us, no active stake yet → pending",
			snap: &chain.Snapshot{AccountStatus: "registered", DelegatedPoolID: us, ActiveStakePoolID: ""},
			want: StatePending,
		},
		{
			name: "promoted: active stake now counts for us → active",
			snap: &chain.Snapshot{AccountStatus: "registered", DelegatedPoolID: us, ActiveStakePoolID: us, ActiveStakeLovelace: "5000000"},
			want: StateActive,
		},
		{
			name: "leaving tail: live delegation moved away but active stake still ours → still active",
			snap: &chain.Snapshot{AccountStatus: "registered", DelegatedPoolID: "pool1other", ActiveStakePoolID: us},
			want: StateActive,
		},
		{
			name: "converged: live + active both elsewhere → none",
			snap: &chain.Snapshot{AccountStatus: "registered", DelegatedPoolID: "pool1other", ActiveStakePoolID: "pool1other"},
			want: StateNone,
		},
		{
			name: "active elsewhere, undelegated live → none",
			snap: &chain.Snapshot{AccountStatus: "registered", DelegatedPoolID: "", ActiveStakePoolID: "pool1other"},
			want: StateNone,
		},
		{
			name: "live-delegating to us but not registered → none (not a valid pending)",
			snap: &chain.Snapshot{AccountStatus: "not_registered", DelegatedPoolID: us},
			want: StateNone,
		},
		{
			name: "amount is irrelevant to state: tiny active stake with us is still active",
			snap: &chain.Snapshot{AccountStatus: "registered", DelegatedPoolID: us, ActiveStakePoolID: us, ActiveStakeLovelace: "1"},
			want: StateActive,
		},
		{
			name: "nil snapshot → none",
			snap: nil,
			want: StateNone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DeriveState(c.snap, us); got != c.want {
				t.Fatalf("DeriveState = %q, want %q", got, c.want)
			}
		})
	}

	// Empty pool id is a defensive guard → never a member.
	if got := DeriveState(&chain.Snapshot{ActiveStakePoolID: "pool1any"}, ""); got != StateNone {
		t.Fatalf("empty poolID: got %q, want none", got)
	}
}
