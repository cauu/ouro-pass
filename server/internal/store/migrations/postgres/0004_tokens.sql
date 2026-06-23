-- §4 Tokens & credentials.
CREATE TABLE IssuedToken (
    jti                   TEXT PRIMARY KEY,
    stake_credential_hash TEXT NOT NULL,
    kind                  TEXT NOT NULL,
    audience              TEXT NOT NULL,
    kid                   TEXT NOT NULL,
    client_id             TEXT,
    status                TEXT NOT NULL,
    issued_at             TEXT NOT NULL,
    expires_at            TEXT NOT NULL,
    redeemed_at           TEXT,
    revoked_at            TEXT
);
CREATE INDEX idx_issuedtoken_sch ON IssuedToken(stake_credential_hash);

CREATE TABLE RefreshGrant (
    refresh_grant_id      TEXT PRIMARY KEY,
    stake_credential_hash TEXT NOT NULL,
    audience              TEXT NOT NULL,
    client_type           TEXT NOT NULL,
    bound_device_pubkey   BYTEA,
    client_id             TEXT,
    status                TEXT NOT NULL,
    rotated_from          TEXT,
    created_at            TEXT NOT NULL,
    expires_at            TEXT,
    last_used_at          TEXT
);
CREATE INDEX idx_refreshgrant_sch ON RefreshGrant(stake_credential_hash);

CREATE TABLE AuthorizationCode (
    code                  TEXT PRIMARY KEY,
    client_id             TEXT NOT NULL,
    stake_credential_hash TEXT NOT NULL,
    aud                   TEXT NOT NULL,
    scope                 TEXT,
    redirect_uri          TEXT NOT NULL,
    code_challenge        TEXT,
    expires_at            TEXT NOT NULL,
    consumed_at           TEXT,
    created_at            TEXT NOT NULL
);

CREATE TABLE ActivationCode (
    code                  TEXT PRIMARY KEY,
    stake_credential_hash TEXT NOT NULL,
    channel_type          TEXT NOT NULL,
    status                TEXT NOT NULL,
    expires_at            TEXT NOT NULL,
    consumed_at           TEXT,
    created_at            TEXT NOT NULL
);

CREATE TABLE AuthNonce (
    nonce          TEXT PRIMARY KEY,
    purpose        TEXT NOT NULL,
    bound_key_hash TEXT,
    expires_at     TEXT NOT NULL,
    consumed_at    TEXT,
    created_at     TEXT NOT NULL
);
