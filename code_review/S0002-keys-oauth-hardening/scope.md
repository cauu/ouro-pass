# Review Scope — S0002 keys & OAuth-client hardening

## Case
Case 1 (spec's changes) **narrowed to this session's work**. The active spec
`docs/specs/20260624T2355-S0002-ouropass-web-frontend.md` is large (the whole
Admin SPA); this review targets only the acceptance-found follow-ups and OAuth
client hardening added in the current session, not the entire SPA.

## Base / range
- Base ref: `b9f87b8` (HEAD at session start, prior work already delivered)
- Range: `b9f87b8..HEAD` (HEAD = `93c5e5e`)
- Diff scope: `git diff b9f87b8..HEAD -- server web`
- Unrelated uncommitted worktree noise (`.gitignore`, deleted `adminui/dist/.gitkeep`) is **excluded**.

## Commits under review
- `518b016` p3-1-fix2 admin client GET `no-store` (JWKS refresh after key gen/rotate)
- `c5a5d7f` p3-1-fix3 surface JWKS key `status` (active signing key visible)
- `0205d8a` p3-1-fix4 merge key Generate/Rotate into one context-aware button
- `9f29f67` p5-2 owner-driven manual retire for rotating signing keys
- `e0664a5` p6-1 system-generate OAuth `client_id`
- `e8ae88a` p6-2 drop dead OAuth client fields `party` + `allowed_scopes` (migration 0007)
- `990c42d` p6-3 mandate PKCE for all clients, drop `pkce_required` (migration 0008)
- `93c5e5e` p6-4 copy `client_id` + regenerate client secret

## Diff stat
```
 server/internal/core/keys/keys.go                  |  20 +++
 server/internal/core/keys/keys_test.go             |  36 +++++
 server/internal/core/oauth/oauth.go                |  13 +-
 server/internal/core/oauth/token.go                |  17 +--
 server/internal/domain/oauthclient.go              |  16 +--
 server/internal/httpapi/handlers_admin_resources.go| 104 +++++++++++---
 server/internal/store/repo_oauthclient.go          |  48 +++----
 server/internal/store/migrations/{sqlite,postgres}/0007,0008  (4 files)
 web/src/api/admin.ts                               |  17 +++
 web/src/api/client.ts                              |   6 +
 web/src/features/clients/ClientsPage.tsx           | 152 +++++++++++-------
 web/src/features/keys/KeysPage.tsx                 |  66 ++++++---
 web/src/lib/types.ts                               |   8 +-
 (+ test files)
 27 files changed, 510 insertions(+), 211 deletions(-)
```

## Reviewers
- claude (always)
- cursor (auto model) — available, logged in
- codex — **SKIPPED** per user (local Codex not recovered this session)

## Spec standard (acceptance items being verified)
The changes map to spec acceptance **TC-3** (business pages consume backend
contracts correctly). Item-level acceptance from the spec:

- **p3-1-fix2** — admin GET `cache:"no-store"`; KeysPage JWKS list refreshes
  immediately after generate/rotate (server JWKS `Cache-Control` for verifiers
  unchanged).
- **p3-1-fix3** — `Jwk.status` surfaced; Keys table marks the `active` signing key.
- **p3-1-fix4** — Generate/Rotate merged into one button keyed on `hasActiveKey`.
- **p5-2** — `keys.Service.Retire(kid)`: only `rotating` keys retire
  (`ErrNotRotating`/`ErrNotFound`); `POST /keys/issuer/{kid}/retire` owner+step-up,
  404/409 mapping, audit `issuer_key.retire`.
- **p6-1** — `client_id` system-generated (`op-client-`+RandomToken(9)); request
  `client_id` ignored; `name` required; response returns generated id.
- **p6-2** — `party` + `allowed_scopes` dropped end-to-end incl. migration 0007
  (DROP COLUMN). They were dead config (party unread; allowed_scopes unenforced).
- **p6-3** — PKCE mandatory for ALL clients: authorize always requires
  `code_challenge`; `authenticateClient` verifies PKCE for everyone AND secret for
  confidential; `pkce_required` dropped incl. migration 0008.
- **p6-4** — `POST /oauth-clients/{client_id}/secret` (owner+step-up): regenerate
  confidential secret (404/409 guards, audit `oauth_client.secret_regenerate`,
  returned once); frontend Copy ID + Regenerate secret. Secrets stay hashed
  (no reversible storage).

### Reviewers should pay special attention to
- PKCE correctness (p6-3): is the authorize→token binding actually closed for
  confidential clients now? Any path that issues/exchanges a code without a
  verified challenge?
- Migrations 0007/0008: `DROP COLUMN` correctness and forward-only safety; the
  dropped columns were `NOT NULL` with no default.
- Secret regenerate (p6-4): authz (owner+step-up), error mapping, no plaintext
  leakage beyond the one-time response; audit coverage.
- Key lifecycle (p5-2): can `Retire` ever drop the active signing key or a key
  still needed for verification?
- Frontend: react-query cache correctness, controlled dialogs, missing-field
  guards, one-time-secret reveal handling.
