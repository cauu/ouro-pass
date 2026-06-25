-- S0004 p4-1: remove the rules engine. Business policy (thresholds → access) is
-- now the relying party's; the issuer keeps only a thin first-party tier mapping
-- in PoolConfig.tier_rules. The MembershipRule table is obsolete.
DROP TABLE IF EXISTS MembershipRule;
