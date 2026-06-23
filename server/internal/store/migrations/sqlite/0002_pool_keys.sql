-- §2 Pool & signing keys.
CREATE TABLE PoolConfig (
    pool_id      TEXT PRIMARY KEY,
    ticker       TEXT NOT NULL DEFAULT '',
    name         TEXT,
    metadata_url TEXT,
    network      TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE TABLE IssuerKey (
    kid                   TEXT PRIMARY KEY,
    public_key            BLOB NOT NULL,
    encrypted_private_key BLOB NOT NULL,
    status                TEXT NOT NULL,
    valid_from            TEXT,
    valid_until           TEXT,
    created_at            TEXT NOT NULL,
    retired_at            TEXT
);
CREATE INDEX idx_issuerkey_status ON IssuerKey(status);
