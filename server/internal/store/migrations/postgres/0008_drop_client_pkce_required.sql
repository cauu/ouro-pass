-- p6-3: drop OAuthClient.pkce_required. PKCE is now mandatory for every client
-- (OAuth 2.1) — enforced unconditionally at authorize/token — so the per-client
-- flag is obsolete.
ALTER TABLE OAuthClient DROP COLUMN pkce_required;
