-- S0004 p2-2: the active-membership cache must carry epochs_active so a cache hit
-- can reconstruct the full active snapshot (epochs_active drives member_since and
-- token claims) without a chain round-trip.
ALTER TABLE StakeSnapshotCache ADD COLUMN epochs_active INTEGER NOT NULL DEFAULT 0;
