package attestor

import (
	"encoding/json"
	"reflect"
	"testing"

	"ouro-pass/server/internal/domain"
)

// TestDistinctNetworks covers S0014 p1-3: distinct networks of active pool_stake attestors,
// first-seen order; empty network → mainnet; non-pool kinds ignored.
func TestDistinctNetworks(t *testing.T) {
	pool := func(net string) domain.AttestorConfig {
		return domain.AttestorConfig{Kind: KindPoolStake, Params: json.RawMessage(`{"pool_id":"pool1x","network":"` + net + `"}`)}
	}
	cfgs := []domain.AttestorConfig{
		pool("mainnet"),
		pool("preprod"),
		pool("mainnet"), // dup
		{Kind: "nft"},    // ignored
		{Kind: KindPoolStake, Params: json.RawMessage(`{"pool_id":"p"}`)}, // empty net → mainnet (dup)
	}
	got := DistinctNetworks(cfgs)
	if want := []string{"mainnet", "preprod"}; !reflect.DeepEqual(got, want) {
		t.Errorf("DistinctNetworks = %v, want %v", got, want)
	}
	if got := DistinctNetworks(nil); len(got) != 0 {
		t.Errorf("empty cfgs → %v, want []", got)
	}
}
