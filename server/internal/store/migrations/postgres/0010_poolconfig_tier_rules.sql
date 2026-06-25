-- S0004 p4-1: the thin first-party tier mapping (state+active_stake → tier), used
-- only by the issuer's own channels (Telegram/Push). Replaces the deleted rules
-- engine's tier judgment. Ordered JSON array, first match wins.
ALTER TABLE PoolConfig ADD COLUMN tier_rules TEXT NOT NULL DEFAULT '[]';
