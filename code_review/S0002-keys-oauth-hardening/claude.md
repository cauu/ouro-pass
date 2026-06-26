# Claude Review — S0002-keys-oauth-hardening

Scope reviewed: case 1 (spec changes, session-scoped)
Base/diff: b9f87b8..HEAD | Files: 27, Lines: +510/−211
Spec standard: docs/specs/20260624T2355-S0002-ouropass-web-frontend.md

## Assessment

Overall: **APPROVE** (with minor follow-ups)

The PKCE-for-all change is implemented correctly and — importantly — closes the
real gap (confidential clients now verify PKCE at token exchange, not just secret;
`authenticateClient` is genuinely invoked in `tokenAuthCode` before mint). Key
lifecycle, secret regenerate, dead-field removal, and migrations are sound and
well-tested. Findings below are all P2/P3 hardening/footgun notes — nothing blocks.

## Findings

### P0 — Critical
- none.

### P1 — High
- none.

### P2 — Medium
- **[server/internal/core/oauth/oauth.go:119]** No PKCE method/format validation.
  - Issue: `Authorize` accepts ANY non-empty `code_challenge`; there is no
    `code_challenge_method` handling and no length/charset check. The server only
    supports S256 (verified via `pkceS256` at token time), but a client that sends
    a `plain`-method challenge (or a malformed one) is not rejected at authorize —
    it silently receives a code that can never be redeemed.
  - Risk: confusing failure mode (opaque `invalid_grant` at token time instead of
    a clear `invalid_request` at authorize); minor RFC 7636 non-compliance.
  - Suggested fix: if a `code_challenge_method` param is accepted, reject anything
    but `S256` with `invalid_request`; optionally validate the challenge is 43
    base64url chars.

### P3 — Low
- **[server/internal/httpapi/handlers_admin_resources.go:310-325]** Register uses
  `Upsert` (INSERT … ON CONFLICT DO UPDATE) with a freshly random `client_id`.
  - Issue: on a (astronomically unlikely, 72-bit) id collision, registration would
    silently overwrite an existing client instead of erroring.
  - Risk: negligible in practice; semantically "register" should never clobber.
  - Suggested fix: use an insert-only path for register (fail on conflict), or
    accept given the entropy. Document the choice.
- **[server/internal/core/keys/keys.go:124-134]** `Retire` is Get-then-SetStatus
  (non-atomic).
  - Issue: TOCTOU — a concurrent rotate/retire could act on a stale status.
  - Risk: very low (owner + step-up, single-operator admin action).
  - Suggested fix: a status-guarded UPDATE (`… WHERE kid=? AND status='rotating'`)
    would make it atomic; optional.
- **[web/src/features/clients/ClientsPage.tsx:176-181]** `CopyButton` toasts
  "Copied" unconditionally even when `navigator.clipboard` is undefined
  (non-secure context / older browser) — `?.` no-ops the write but the success
  toast still fires.
  - Suggested fix: only toast on a resolved `writeText`, or feature-detect.

## Spec Compliance
- **p3-1-fix2** (admin GET no-store): met — `web/src/api/client.ts:31` adds
  `cache:"no-store"` in the shared `request()`; verifier-facing server cache
  untouched.
- **p3-1-fix3** (JWKS status surfaced): met — `Jwk.status` + KeysPage Status column
  with `active (signing)` badge.
- **p3-1-fix4** (Generate/Rotate merged): met — single button keyed on
  `hasActiveKey = keys.some(status==='active')`.
- **p5-2** (manual retire): met — `keys.Retire` guards `ErrNotRotating`/
  `ErrNotFound`; `POST /keys/issuer/{kid}/retire` owner+step-up; 404/409 mapping;
  audit `issuer_key.retire`; `TestRetire` covers reject-active / unknown / success
  / re-retire.
- **p6-1** (system-generated client_id): met — `op-client-`+RandomToken(9); request
  `client_id` ignored; `name` required; test asserts supplied id ignored. (See P3
  Upsert note.)
- **p6-2** (drop party + allowed_scopes): met — end-to-end incl. migration 0007;
  confirmed both were unread/unenforced.
- **p6-3** (mandatory PKCE, drop pkce_required): met — authorize always requires
  `code_challenge`; `authenticateClient` verifies PKCE for all + secret for
  confidential (gap closed); migration 0008. (See P2 method-validation note.)
- **p6-4** (copy id + regenerate secret): met — `POST /oauth-clients/{id}/secret`
  owner+step-up, 404/409 guards, audit `oauth_client.secret_regenerate`, returned
  once; secrets stay hashed (no reversible storage); frontend Copy ID + per-row
  Regenerate (confidential only).
- **TC-3**: met — pages consume backend contracts; secret-hash never leaked in list
  (`adminListClients` nils `ClientSecretHash`, unchanged).
- Scope drift: none. Backend p5/p6 work is within the spec's appended items.

## Removal / Iteration candidates
- `RetireRotating(olderThan)` (keys.go) remains with no production caller — spec
  records this as deferred p5-1 (auto-retire worker not adopted). Acceptable; it's
  documented, and `Retire` is the chosen manual path.

## Notes / residual risk
- Behavior change for downstream: confidential clients must now send BOTH
  `client_secret` AND a valid `code_verifier` at token exchange — existing
  integrations that skipped PKCE will start getting `invalid_grant`. Operationally
  expected (spec records it) but worth flagging to integrators before deploy.
- Migrations 0007/0008 use `ALTER TABLE … DROP COLUMN`; verified safe here
  (modernc SQLite 3.45+/pgx; dropped columns carried no index). store + e2e tests
  run these migrations and pass.
