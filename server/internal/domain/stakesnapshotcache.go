package domain

import "time"

// StakeSnapshotCache caches raw on-chain stake snapshots (not eligibility
// conclusions); eligibility is recomputed by the rule engine (§3.3). Optional.
type StakeSnapshotCache struct {
	StakeCredentialHash string
	SnapshotEpoch       int64
	DelegatedPoolID     *string
	ActiveStakeLovelace *string // numeric(20) carried as decimal string (C4/D6)
	RewardsLovelace     *string
	Source              string // node_lsq | db_sync | koios | blockfrost
	FetchedAt           time.Time
}
