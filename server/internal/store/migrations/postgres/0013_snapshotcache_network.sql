-- S0006 p5-1: generalize the active-membership cache. It becomes pool-agnostic
-- (delegated_pool_id now holds the credential's REAL active pool, so all
-- pool_stake attestors share one cached snapshot) and network-scoped (a deployment
-- may serve attestors on different networks). The cache is regenerable, so we drop
-- and recreate rather than migrate rows.
DROP TABLE IF EXISTS StakeSnapshotCache;
CREATE TABLE StakeSnapshotCache (
    stake_credential_hash TEXT NOT NULL,
    network               TEXT NOT NULL,
    snapshot_epoch        INTEGER NOT NULL,
    delegated_pool_id     TEXT,
    active_stake_lovelace TEXT,
    rewards_lovelace      TEXT,
    epochs_active         INTEGER NOT NULL DEFAULT 0,
    source                TEXT NOT NULL,
    fetched_at            TEXT NOT NULL,
    PRIMARY KEY (stake_credential_hash, network)
);
