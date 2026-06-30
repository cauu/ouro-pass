package attestor

import (
	"encoding/json"

	"ouro-pass/server/internal/domain"
)

// DistinctNetworks returns the set of networks used by active pool_stake attestor configs,
// in first-seen order (S0014 p1-3). An empty per-attestor network defaults to "mainnet"
// (the admin form default). Used to drive per-network epoch watching in reconciliation.
func DistinctNetworks(cfgs []domain.AttestorConfig) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, c := range cfgs {
		if c.Kind != KindPoolStake {
			continue
		}
		var p PoolStakeParams
		if json.Unmarshal(c.Params, &p) != nil {
			continue
		}
		net := p.Network
		if net == "" {
			net = "mainnet"
		}
		if !seen[net] {
			seen[net] = true
			out = append(out, net)
		}
	}
	return out
}
