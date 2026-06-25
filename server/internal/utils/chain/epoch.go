package chain

import "time"

// epochParams pins a network's epoch clock: the unix time of epoch 0 and the
// epoch length in seconds. Cardano epochs are uniform within a network, so the
// current epoch is pure arithmetic — no chain round-trip (S0004 D7). Values are
// the well-known genesis parameters; confirm against live /tip in integration (R1).
//
//	mainnet: Byron genesis 2017-09-23T21:44:51Z, 432000s/epoch (Byron & Shelley
//	         are both 5 days, so one linear formula spans both eras).
//	preprod: genesis 2022-06-01T00:00:00Z, 432000s/epoch.
//	preview: genesis 2022-10-25T00:00:00Z, 86400s/epoch (1-day epochs).
type epochParams struct {
	genesisUnix int64
	lengthSec   int64
}

var epochByNetwork = map[string]epochParams{
	"mainnet": {genesisUnix: 1506203091, lengthSec: 432000},
	"preprod": {genesisUnix: 1654041600, lengthSec: 432000},
	"preview": {genesisUnix: 1666656000, lengthSec: 86400},
}

// CurrentEpoch computes the current Cardano epoch for a network locally (no I/O).
// ok=false for an unknown network or a time before genesis — callers then fall
// back to treating every lookup as a cache miss (always-live, slower but correct;
// a wrong/unknown epoch can never produce a stale cache hit because the stored
// snapshot_epoch — the real /tip epoch at cache time — won't match).
func CurrentEpoch(network string, now time.Time) (uint64, bool) {
	p, ok := epochByNetwork[network]
	if !ok {
		return 0, false
	}
	t := now.Unix()
	if t < p.genesisUnix {
		return 0, false
	}
	return uint64((t - p.genesisUnix) / p.lengthSec), true
}
