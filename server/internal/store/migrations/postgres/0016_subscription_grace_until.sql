-- S0019 p1-2: membership-driven expiry uses a grace deadline. grace_until is NULL
-- whenever the session is not in grace (the sole "not in grace" signal); it is set
-- to now+GRACE the first reconcile that sees state==none and cleared back to NULL
-- once membership is re-observed. Distinct from the informational expires_at
-- (= last_verified_at + TTL), which is never enforced.
ALTER TABLE SubscriptionSession ADD COLUMN grace_until TEXT;
