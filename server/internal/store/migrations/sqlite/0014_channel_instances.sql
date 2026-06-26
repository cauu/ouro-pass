-- S0005 p1-1: promote channel instances to addressable first-class entities.
-- ChannelConfig gains a human label `name`, unique within a (pool_id, channel_type),
-- so a pool may run N instances of one platform. Subscriptions and activation codes
-- gain `channel_id`; the subscription unique key moves from
-- (pool_id, channel_type, channel_user_id) to (channel_id, channel_user_id) so the
-- same channel user may subscribe to several instances of one platform. Existing
-- single-telegram data is backfilled to its instance (the 'default' instance, D6).
ALTER TABLE ChannelConfig ADD COLUMN name TEXT NOT NULL DEFAULT 'default';
CREATE UNIQUE INDEX idx_channelconfig_pool_type_name ON ChannelConfig(pool_id, channel_type, name);

ALTER TABLE ActivationCode ADD COLUMN channel_id TEXT NOT NULL DEFAULT '';
UPDATE ActivationCode SET channel_id = COALESCE((
    SELECT c.channel_id FROM ChannelConfig c
    WHERE c.channel_type = ActivationCode.channel_type
    ORDER BY c.updated_at DESC LIMIT 1), '')
WHERE channel_id = '';

-- SQLite cannot drop an inline UNIQUE constraint, so rebuild the table with the
-- new key and backfill channel_id from the matching (pool, type) instance.
CREATE TABLE SubscriptionSession_new (
    session_id            TEXT PRIMARY KEY,
    pool_id               TEXT NOT NULL,
    stake_credential_hash TEXT NOT NULL,
    channel_id            TEXT NOT NULL DEFAULT '',
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
    UNIQUE (channel_id, channel_user_id)
);
INSERT INTO SubscriptionSession_new (session_id, pool_id, stake_credential_hash, channel_id, channel_type, channel_user_id, channel_account_id, status, tier, topics, entitlements, created_at, last_verified_at, expires_at, cancelled_at)
SELECT s.session_id, s.pool_id, s.stake_credential_hash,
    COALESCE((SELECT c.channel_id FROM ChannelConfig c WHERE c.pool_id = s.pool_id AND c.channel_type = s.channel_type ORDER BY c.updated_at DESC LIMIT 1), ''),
    s.channel_type, s.channel_user_id, s.channel_account_id, s.status, s.tier, s.topics, s.entitlements, s.created_at, s.last_verified_at, s.expires_at, s.cancelled_at
FROM SubscriptionSession s;
DROP TABLE SubscriptionSession;
ALTER TABLE SubscriptionSession_new RENAME TO SubscriptionSession;
CREATE INDEX idx_subscription_sch ON SubscriptionSession(stake_credential_hash);
