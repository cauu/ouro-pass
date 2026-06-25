-- p6-2: drop OAuthClient.party and .allowed_scopes. Both were dead config —
-- party was never read by any logic, and allowed_scopes was never enforced at
-- authorize/token. Removed to keep client registration to load-bearing fields.
ALTER TABLE OAuthClient DROP COLUMN party;
ALTER TABLE OAuthClient DROP COLUMN allowed_scopes;
