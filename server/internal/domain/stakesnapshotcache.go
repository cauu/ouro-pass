package domain

import "time"

// StakeSnapshotCache caches the active-membership snapshot for one credential
// (S0004 §2.3): a row always means "at SnapshotEpoch this credential was active
// with our pool". Only `active` is cached (epoch-stable); pending/none are
// computed live. The cached facts let a hit reconstruct the full active snapshot
// without a chain round-trip.
type StakeSnapshotCache struct {
	StakeCredentialHash string
	SnapshotEpoch       int64
	DelegatedPoolID     *string // the active pool for this row (= our pool for cached active rows)
	ActiveStakeLovelace *string // numeric(20) carried as decimal string (C4)
	RewardsLovelace     *string
	EpochsActive        int64 // trailing consecutive active epochs at SnapshotEpoch
	Source              string // node_lsq | db_sync | koios | blockfrost
	FetchedAt           time.Time
}
