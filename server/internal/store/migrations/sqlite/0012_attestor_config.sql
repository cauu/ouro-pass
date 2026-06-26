-- S0006 p1-2: generalize "the served pool" into a SET of attestor configs, and
-- move the first-party tier_rules out of PoolConfig into a global singleton.
-- PoolConfig stays for now — old read paths (oauth.firstPartyTier, chain network)
-- cut over in later S0006 items; here we only add the new model and backfill it.
CREATE TABLE AttestorConfig (
    attestor_id TEXT PRIMARY KEY,
    kind        TEXT NOT NULL,
    label       TEXT NOT NULL,
    params      TEXT NOT NULL,
    status      TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (kind, label)
);

CREATE TABLE IssuerConfig (
    id         TEXT PRIMARY KEY,
    tier_rules TEXT NOT NULL DEFAULT '[]',
    updated_at TEXT NOT NULL
);

-- Backfill: each existing pool becomes one pool_stake attestor (network/ticker/
-- name fold into params); its tier_rules lift to the issuer-global config
-- (last pool wins — deployments are single-pool today).
INSERT INTO AttestorConfig (attestor_id, kind, label, params, status, created_at, updated_at)
SELECT 'pool_stake:' || pool_id, 'pool_stake', COALESCE(NULLIF(ticker, ''), pool_id),
       json_object('pool_id', pool_id, 'network', network, 'ticker', ticker, 'name', COALESCE(name, '')),
       'active', created_at, updated_at
FROM PoolConfig;

INSERT INTO IssuerConfig (id, tier_rules, updated_at)
SELECT 'default', tier_rules, updated_at FROM PoolConfig LIMIT 1;
