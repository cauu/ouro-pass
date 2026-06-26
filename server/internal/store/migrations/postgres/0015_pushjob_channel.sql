-- S0005 p3-1: a push job may target a single channel instance. NULL keeps the
-- legacy type-level fan-out across the default instance.
ALTER TABLE PushJob ADD COLUMN channel_id TEXT;
