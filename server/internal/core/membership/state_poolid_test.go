package membership

import (
	"strings"
	"testing"

	"ouro-pass/server/internal/utils/chain"
)

// TestDeriveState_PoolIDFormatAgnostic covers S0014 p4-1 / TC-8: a pool configured as
// hex matches the bech32 form Koios returns (and vice versa), while a non-delegator still
// resolves to StateNone (no loosening of who qualifies).
func TestDeriveState_PoolIDFormatAgnostic(t *testing.T) {
	hexPool := strings.Repeat("ab", 28)
	bechPool := chain.CanonicalPoolID(hexPool) // the form koios returns
	otherPool := chain.CanonicalPoolID(strings.Repeat("cd", 28))

	// configured hex, snapshot bech32 (active) → eligible
	if got := DeriveState(&chain.Snapshot{ActiveStakePoolID: bechPool}, hexPool); got != StateActive {
		t.Errorf("hex-configured vs bech32 active snapshot = %q, want active", got)
	}
	// configured bech32, snapshot hex (pending) → eligible
	if got := DeriveState(&chain.Snapshot{AccountStatus: "registered", DelegatedPoolID: hexPool}, bechPool); got != StatePending {
		t.Errorf("bech32-configured vs hex delegated snapshot = %q, want pending", got)
	}
	// a different pool stays none — no loosening
	if got := DeriveState(&chain.Snapshot{ActiveStakePoolID: otherPool}, hexPool); got != StateNone {
		t.Errorf("non-delegator = %q, want none", got)
	}
}
