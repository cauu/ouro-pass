-- §3 Rules & identity.
CREATE TABLE MembershipRule (
    rule_id      TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    rule_config  TEXT NOT NULL,
    tier         TEXT NOT NULL,
    entitlements TEXT NOT NULL,
    priority     INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX idx_rule_status_priority ON MembershipRule(status, priority);

CREATE TABLE StakeSnapshotCache (
    stake_credential_hash TEXT PRIMARY KEY,
    snapshot_epoch        INTEGER NOT NULL,
    delegated_pool_id     TEXT,
    active_stake_lovelace TEXT,
    rewards_lovelace      TEXT,
    source                TEXT NOT NULL,
    fetched_at            TEXT NOT NULL
);

CREATE TABLE Blacklist (
    stake_credential_hash TEXT PRIMARY KEY,
    reason                TEXT,
    created_at            TEXT NOT NULL
);
