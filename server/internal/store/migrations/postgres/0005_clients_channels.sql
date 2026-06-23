-- §5 Clients, §6 Channels & subscriptions.
CREATE TABLE OAuthClient (
    client_id          TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    client_type        TEXT NOT NULL,
    client_secret_hash TEXT,
    party              TEXT NOT NULL,
    redirect_uris      TEXT NOT NULL,
    allowed_audiences  TEXT NOT NULL,
    allowed_scopes     TEXT NOT NULL,
    pkce_required      INTEGER NOT NULL DEFAULT 0,
    status             TEXT NOT NULL,
    created_at         TEXT NOT NULL
);

CREATE TABLE ChannelConfig (
    channel_id   TEXT PRIMARY KEY,
    pool_id      TEXT NOT NULL,
    channel_type TEXT NOT NULL,
    config       TEXT NOT NULL,
    status       TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE TABLE SubscriptionSession (
    session_id            TEXT PRIMARY KEY,
    pool_id               TEXT NOT NULL,
    stake_credential_hash TEXT NOT NULL,
    channel_type          TEXT NOT NULL,
    channel_user_id       TEXT NOT NULL,
    channel_account_id    TEXT,
    status                TEXT NOT NULL,
    tier                  TEXT NOT NULL,
    topics                TEXT NOT NULL,
    entitlements          TEXT NOT NULL,
    created_at            TEXT NOT NULL,
    last_verified_at      TEXT NOT NULL,
    expires_at            TEXT NOT NULL,
    cancelled_at          TEXT,
    UNIQUE (pool_id, channel_type, channel_user_id)
);
CREATE INDEX idx_subscription_sch ON SubscriptionSession(stake_credential_hash);
