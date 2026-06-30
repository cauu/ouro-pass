package domain

import "time"

// StakeSnapshotCache caches the active-membership snapshot for one credential on
// one network (S0004 §2.3, generalized in S0006 p5-1): a row always means "at
// SnapshotEpoch this credential was active with DelegatedPoolID". Only `active`
// (active somewhere) is cached — epoch-stable; pending/none are computed live. The
// cached facts let a hit reconstruct the full active snapshot without a chain
// round-trip. Pool-agnostic: every pool_stake attestor on the network shares it.
type StakeSnapshotCache struct {
	StakeCredentialHash string
	Network             string // the chain network this snapshot was read on (S0006 p5-1)
	SnapshotEpoch       int64
	DelegatedPoolID     *string // the credential's REAL active pool for this row (pool-agnostic cache)
	ActiveStakeLovelace *string // numeric(20) carried as decimal string (C4)
	RewardsLovelace     *string
	EpochsActive        int64  // trailing consecutive active epochs at SnapshotEpoch
	Source              string // koios (single origin) | mock (tests)
	FetchedAt           time.Time
}
