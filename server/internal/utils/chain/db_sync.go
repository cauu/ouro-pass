package chain

import "context"

// DBSyncSource queries a self-hosted cardano-db-sync database (the `epoch_stake`
// table) to supply per-credential active stake — still non-third-party. The
// real SQL integration is wired under the `integration` build tag (D5); the
// default build returns ErrNotImplemented so misconfiguration fails fast.
type DBSyncSource struct {
	// DSN, *sql.DB, etc. are added in the integration build.
}

// Name returns "db_sync".
func (d *DBSyncSource) Name() string { return "db_sync" }

// Snapshot is not implemented in the default build.
func (d *DBSyncSource) Snapshot(context.Context, string) (*Snapshot, error) {
	return nil, ErrNotImplemented
}

// Epoch is not implemented in the default build.
func (d *DBSyncSource) Epoch(context.Context) (uint64, error) {
	return 0, ErrNotImplemented
}
