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

ALTER TABLE SubscriptionSession ADD COLUMN channel_id TEXT NOT NULL DEFAULT '';
UPDATE SubscriptionSession s SET channel_id = COALESCE((
    SELECT c.channel_id FROM ChannelConfig c
    WHERE c.pool_id = s.pool_id AND c.channel_type = s.channel_type
    ORDER BY c.updated_at DESC LIMIT 1), '');
-- The old inline UNIQUE (pool_id, channel_type, channel_user_id) carries the
-- predictable Postgres auto-name; drop it and add the instance-scoped key.
ALTER TABLE SubscriptionSession DROP CONSTRAINT IF EXISTS subscriptionsession_pool_id_channel_type_channel_user_id_key;
ALTER TABLE SubscriptionSession ADD CONSTRAINT subscriptionsession_channel_id_channel_user_id_key UNIQUE (channel_id, channel_user_id);
