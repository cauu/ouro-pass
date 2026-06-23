# Cursor Agent (Auto) Review — S0001-poolops-issuer-backend

Scope reviewed: case 1 spec-changes (whole server/ at HEAD)
Base/diff: 19da9ae..HEAD -- server/  |  Files: 75 non-test, ~6062 LOC
Spec standard: docs/specs/20260623T0041-S0001-poolops-issuer-backend.md

## Assessment
Overall: REQUEST_CHANGES
One-line rationale: Core OAuth/wallet/crypto scaffolding is sound and well-tested on SQLite+mock chain, but production chain adapters cannot resolve eligibility (sch vs stake-address mismatch), refresh rotation lacks atomicity, public-client PoP is not enforced on refresh, and the push scheduler is never wired at runtime.

## Findings
### P0 — Critical
- **[server/internal/utils/chain/koios.go:50-58, server/internal/utils/chain/node_lsq.go:66-75]** Chain adapters query with `stake_credential_hash` instead of bech32 stake address
  - Issue: `walletauth` and OAuth persist identity as `hex(blake2b224(stake_vkey))`, but `KoiosSource.Snapshot` posts it as `_stake_addresses` and `NodeLSQSource.Snapshot` passes it to `cardano-cli query stake-address-info --address`. Koios comments say bech32 is required; neither adapter maps hash → address.
  - Risk: With `OUROPASS_CHAIN_KIND=koios|node_lsq`, live snapshots return empty/wrong delegation; `evaluate` rejects everyone or mis-evaluates. The issuer cannot operate in production outside `mock`.
  - Suggested fix: Introduce a pure mapping layer (e.g. stake vkey hex → stake address at challenge time, or credential-hash → address table/cache) and pass bech32 stake addresses to adapters; add integration tests with real Koios/node fixtures.

### P1 — High
- **[server/internal/core/oauth/token.go:60-117, server/internal/store/repo_refreshgrant.go:59-66]** Refresh rotation is not atomic — concurrent refresh can mint multiple active grants
  - Issue: `tokenRefresh` reads grant status `active`, later calls `SetStatus(..., GrantRotated)` (unconditional `UPDATE`), then `mint`. Two parallel requests with the same refresh token can both pass the `GrantActive` check and each create a new active grant + access token.
  - Risk: Violates single-use refresh semantics; expands blast radius of a stolen refresh token; theft-detection (`RevokeChain` on rotated replay) does not trigger because both consumers rotate from `active`, not from `rotated`.
  - Suggested fix: Single transaction with `UPDATE RefreshGrant SET status='rotated' WHERE refresh_grant_id=? AND status='active'` and check `RowsAffected()==1`; only the winner calls `mint`. Loser returns `invalid_grant`.

- **[server/internal/core/oauth/token.go:106-117, server/internal/core/oauth/token.go:190-238]** Refresh marks grant `rotated` before `mint` completes; `mint` is not transactional
  - Issue: `SetStatus(rotated)` precedes `mint`, and `mint` separately creates `IssuedToken` then `RefreshGrant` without `WithTx`.
  - Risk: `mint` failure after rotation bricks the user's session (no valid refresh, old grant unusable). Partial `mint` can leave a ledger access token without a matching refresh grant.
  - Suggested fix: Wrap rotate+mint in one `store.WithTx`; rotate only after all mint steps succeed, or use compensating rollback on failure.

- **[server/internal/core/oauth/token.go:60-94, server/internal/core/oauth/token.go:210-216]** Public-client refresh does not enforce device PoP (`cnf.jkt` / DPoP)
  - Issue: Comment says "DPoP deferred per D7", but refresh path only checks `client_secret` for confidential clients. For public clients, a stolen refresh token is exchanged with no `device_pubkey` proof and no DPoP header validation; `BoundDevicePubkey` is copied forward passively.
  - Risk: C6 public-client PoP is illusory after initial issuance; bearer refresh tokens are fully bearer until expiry/theft-detection.
  - Suggested fix: On refresh for `ClientPublic`, require `device_pubkey` (or DPoP proof) matching `grant.BoundDevicePubkey` / `cnf.jkt` thumbprint; reject mismatch with `invalid_grant`.

- **[server/cmd/issuer/main.go:147-158]** Push scheduler worker is never started
  - Issue: `main` starts Telegram + reconciliation goroutines but does not instantiate `push.Scheduler` or poll `PushJob` rows. Admin `POST /api/admin/push/jobs` only inserts `scheduled` jobs.
  - Risk: TC-10 behavior exists only in unit tests; operators creating push jobs see no delivery at runtime.
  - Suggested fix: Add a push worker loop (poll `PushJob` where `status=scheduled`, call `Scheduler.Run`, respect ctx shutdown) sharing the Telegram `Sender` transport when `channel_type=telegram`.

- **[server/internal/core/rules/engine.go:27-35, server/internal/core/oauth/oauth.go:161-171]** `min_active_epochs` / delegation-age rules never apply in production path
  - Issue: `InputFromSnapshot` hardcodes `EpochsDelegated: -1`; no adapter populates delegation age. `satisfies` skips `MinActiveEpochs` when `EpochsDelegated < 0`.
  - Risk: C7 grace / min delegation epoch rules are dead code in live flows; operators configuring `min_active_epochs` get no effect (only stake + pool checks run).
  - Suggested fix: Extend `chain.Snapshot` with `EpochsDelegated` (or compute from epoch history in db_sync), map in `InputFromSnapshot`, document implicit ≤2-epoch lag separately.

- **[server/internal/core/oauth/token.go:211-216]** Invalid `device_pubkey` silently issues public-client token without `cnf.jkt`
  - Issue: `hex.DecodeString(p.devicePubkey)` errors are ignored; token mint proceeds with empty `boundDevice` and no `cnf` claim.
  - Risk: Misconfigured clients receive bearer tokens without PoP binding, defeating C6 intent.
  - Suggested fix: For `ClientPublic`, require valid device key bytes (expected length) and return `invalid_request` on decode failure; reject token issue if `device_pubkey` missing when PKCE was used.

### P2 — Medium
- **[server/internal/store/repo_authnonce.go:41-78, server/internal/store/repo_authorizationcode.go:28-68]** Consume paths lack compare-and-swap on `consumed_at`
  - Issue: Transactions `SELECT` then `UPDATE` without `WHERE consumed_at IS NULL` / `RowsAffected` guard or `SELECT FOR UPDATE`.
  - Risk: Under PostgreSQL `READ COMMITTED`, concurrent nonce/code redemption can both succeed, breaking single-use guarantees (wallet nonce replay → duplicate auth codes).
  - Suggested fix: `UPDATE ... SET consumed_at=? WHERE nonce=? AND consumed_at IS NULL`; if `RowsAffected==0`, return `ErrConsumed`. SQLite MVP is partially shielded by `MaxOpenConns(1)` but PG is not.

- **[server/internal/core/oauth/token.go:91-92, server/internal/core/oauth/token.go:156-157]** Secret / token hash comparisons are not constant-time
  - Issue: `crypto.HashToken(req.ClientSecret) != *client.ClientSecretHash` uses plain string `!=`.
  - Risk: Theoretical timing side-channel on `client_secret` (low practical risk for 256-bit random secrets, but violates stated review bar).
  - Suggested fix: Compare with `subtle.ConstantTimeCompare` on decoded bytes, or HMAC-hash both sides uniformly.

- **[server/internal/httpapi/middleware/middleware.go:128-168]** Idempotency cache is in-process, unscoped
  - Issue: `Idempotency` is a process-local `map` keyed only by `Idempotency-Key` header (no route/client/body scope).
  - Risk: Multi-instance deployments get no idempotency; same key on different endpoints can replay wrong cached response within TTL.
  - Suggested fix: Persist idempotency records (DB) keyed by `(key, route, client_id)` or document single-instance-only and scope cache keys.

- **[server/internal/httpapi/router.go:55-58]** OAuth issuance routes lack IP rate limiting
  - Issue: `/api/connect/authorize` and `/api/oauth/token` are not behind `publicLimit`; only `/api/auth/challenge` and verifier routes are rate-limited.
  - Risk: Unlimited authorize/token attempts enable brute-force of codes/hashes and resource exhaustion against chain `evaluate` (external Koios/node calls per request).
  - Suggested fix: Apply `publicLimit` (or tighter bucket) to issuance plane; consider per-`client_id` limits on token endpoint.

- **[server/internal/utils/crypto/crypto_test.go:76-139]** COSE tests use synthetic vectors only — no captured CIP-30 wallet signatures
  - Issue: Spec TC-3 / R1 call for real-wallet golden vectors; tests mirror the same `makeCOSESign1` construction as production verifier.
  - Risk: Wallet interoperability bugs (CBOR canonicalization, tag handling, header placement) won't be caught until production.
  - Suggested fix: Add `//go:build integration` test file with vectors from Lace/Eternl/etc.; keep synthetic tests for regression.

- **[server/internal/utils/chain/node_lsq.go:47, server/internal/utils/chain/node_lsq.go:59]** `RewardAccountBal` parsed as `int64`
  - Issue: Rewards balance uses `int64` before formatting to string; active stake correctly stays empty/string elsewhere.
  - Risk: C4 violation for large reward values (>2^63-1 lovelace); irrelevant to eligibility today but inconsistent with numeric(20)/`math/big` policy.
  - Suggested fix: Parse rewards as string/`big.Int` like Koios `total_balance`.

- **[server/internal/utils/jose/jose.go:69-84]** `SignActivationToken` exists but activation flow uses short codes (D8), not JWT
  - Issue: Dead signing path; activation handler returns opaque `activation_code`, bot consumes hashed DB row — no `IssuedToken` activation JWT or jti consumption per TC-9 wording.
  - Risk: Spec/design drift; future maintainers may wire the wrong activation mechanism.
  - Suggested fix: Either remove unused `SignActivationToken` or align TC-9/spec text with D8 short-code design explicitly.

### P3 — Low
- **[server/internal/utils/crypto/cose.go:114-133]** COSE `checkAlg` tolerates missing/empty protected `alg`
  - Issue: If protected header is empty or lacks label `1`, verification proceeds without algorithm enforcement (relies solely on `ed25519.Verify`).
  - Risk: Low for Ed25519-only deployment; could accept atypical wallet encodings that omit protected `alg`.
  - Suggested fix: Require `alg=-8` in protected header once interop vectors confirm; or also inspect unprotected header map.

- **[server/internal/utils/chain/chain.go:90-91, server/internal/utils/chain/db_sync.go:16-23]** `db_sync` selectable at startup but always `ErrNotImplemented` at runtime
  - Issue: `NewSource` succeeds for `kind=db_sync`; failures happen on first `Snapshot`/`Epoch`.
  - Risk: Misconfigured deploy passes health check then fails all issuance.
  - Suggested fix: Fail fast in `NewSource` for default build, or return a clear startup error when `db_sync` lacks integration tag.

- **[server/internal/httpapi/handlers_admin.go:64-67]** Admin session cookie always `Secure: true`
  - Issue: Local HTTP admin login (no TLS) won't persist cookie in browsers.
  - Risk: Dev/self-hosted friction only.
  - Suggested fix: Gate `Secure` on config (e.g. `OUROPASS_TLS=true`) or `X-Forwarded-Proto`.

- **[server/internal/core/oauth/oauth.go:138-141]** `Authorize` ignores `DevicePubkey` on auth code record
  - Issue: `AuthorizeRequest.DevicePubkey` is accepted at handler layer but not stored on `AuthorizationCode`; binding happens only at token exchange.
  - Risk: Low given PKCE; document that device binding is token-time only.

## Spec Compliance
- C1 …: met — `go-chi/chi` + `net/http` throughout (`server/internal/httpapi/router.go:12-13`); no Gin/Echo/Fiber.
- C2 …: met — issuance only via `authorization_code` + `refresh_token` on `POST /api/oauth/token` (`server/internal/httpapi/router.go:58`, `server/internal/core/oauth/token.go:46-54`); no license endpoints.
- C3 …: met — service holds issuer signing key + bot token env only (`server/internal/config/config.go:44`, `server/internal/core/keys/keys.go`); owner keys used only for wallet signatures at admin login (`server/internal/core/admin/admin.go:74-78`).
- C4 …: partially-met — rules engine uses `math/big` (`server/internal/core/rules/engine.go:98-110`); DB lovelace as TEXT; but `node_lsq` rewards use `int64` (`server/internal/utils/chain/node_lsq.go:47`).
- C5 …: partially-met — issuer private keys encrypted via `FieldCipher` (`server/internal/core/keys/keys.go:59-61`); `client_secret` stored hashed (`server/internal/httpapi/handlers_admin_resources.go:296-298`); Telegram bot token only in env, not DB-encrypted (`server/internal/config/config.go:44`).
- C6 …: partially-met — EdDSA JWS, `cnf.jkt` on public auth-code exchange (`server/internal/core/oauth/token.go:210-216`); access TTL 24h (`server/cmd/issuer/main.go:57`); activation short code 30m (`server/internal/core/oauth/activation.go:13`); **gaps**: no DPoP/device proof on refresh (`token.go:81-94`), confidential holder-of-key not implemented, invalid device key silently unbound (`token.go:211-216`).
- C7 …: partially-met — reconciliation worker re-evaluates on epoch advance (`server/internal/worker/reconciliation/reconciliation.go:89-108`); snapshot-based `evaluate` (`server/internal/core/oauth/oauth.go:155-171`); **gap**: `min_active_epochs`/grace never fed from chain (`engine.go:33`, `engine.go:113-117`).
- C8 …: met — no Member table; identity `stake_credential_hash`; `sub = base32(HMAC-SHA256)` (`server/internal/utils/crypto/hash.go:31-34`, `server/internal/core/oauth/token.go:199`).
- C9 …: met — JWKS publishes signing pubkeys only (`server/internal/httpapi/handlers_verifier.go:13-31`, `server/internal/utils/jose/jose.go:111-127`); admin owner check uses `OUROPASS_OWNER_KEYS` allowlist (`server/internal/core/admin/admin.go:49-53`, `server/internal/config/config.go:79`).
- C10 …: met — `rules.Evaluate` is pure, sorted, no IO/clock (`server/internal/core/rules/engine.go:56-90`); `keys` service is stateful as allowed (`server/internal/core/keys/keys.go`).
- TC-1 …: met — build/test/smoke evidence in spec §6; `main` graceful shutdown (`server/cmd/issuer/main.go:168-177`), `/healthz` (`server/internal/httpapi/router.go:50`).
- TC-2 …: partially-met — SQLite repo tests pass; PG path documented but not CI-evidenced (`scope.md` / spec §6 TC-2 note).
- TC-3 …: partially-met — COSE unit tests pass (`server/internal/utils/crypto/crypto_test.go`); **no real-wallet golden vectors** (spec D5).
- TC-4 …: met — JWS/JWKS tests (`server/internal/utils/jose/jose_test.go`); endpoint `/.well-known/ouropass/jwks.json`.
- TC-5 …: partially-met — pure function tests incl. priority/grace (`server/internal/core/rules/engine_test.go`); production path never supplies `EpochsDelegated`.
- TC-6 …: met on mock chain — authorize + token flow tested (`server/internal/core/oauth/oauth_test.go`, `token_test.go`); **would fail on real chain adapters** (P0).
- TC-7 …: met — rotation + replay `RevokeChain` tested (`server/internal/core/oauth/refresh_test.go`); concurrent rotation untested.
- TC-8 …: met — key rotate + introspect/revoke tests (`server/internal/core/keys/keys_test.go`, `introspect_test.go`).
- TC-9 …: partially-met — short-code activation + Telegram processor tested (`activation_test.go`, `telegram_test.go`); design is D8 short code not activation JWT (`activation.go:41-55`); bot marks DB code consumed, not jti ledger.
- TC-10 …: partially-met — `push.Scheduler` unit tests pass; **no runtime worker in `main`** (`server/cmd/issuer/main.go`).
- TC-11 …: met — reconciliation tests (`server/internal/worker/reconciliation/reconciliation_test.go`).
- TC-12 …: met — admin auth/RBAC/step-up/audit tests (`admin_test.go`, `handlers_admin_test.go`, `handlers_admin_resources_test.go`).
- Scope drift: `SignActivationToken` / activation JWT types unused (`jose.go:69-84`); push worker not wired; `db_sync` stub; product naming migrated to Ouro Pass (expected per p11-2). Confidential holder-of-key and full DPoP deferred beyond spec C6 wording.

## Removal / Iteration candidates
- `server/internal/utils/jose/jose.go` — `SignActivationToken` + `ActivationClaims` if D8 short-code remains canonical.
- `server/internal/utils/chain/db_sync.go` — default-build stub or gate behind build tag to avoid false startup success.
- `AuthorizeRequest.DevicePubkey` field — unused in `Authorize` (`oauth.go:76-87`); remove or persist on auth code.
- In-memory idempotency (`middleware.go:128-190`) — replace with DB-backed or document MVP-only.

## Notes / residual risk
- SQLite `MaxOpenConns(1)` masks some concurrency bugs present on PostgreSQL; prioritize PG integration tests for consume/refresh races.
- `mock` chain default (`config.go:72`) hides P0 adapter mismatch until operators switch `OUROPASS_CHAIN_KIND`.
- Introspect/revoke are unauthenticated public endpoints (RFC-aligned); rate limiting on verifier plane only partially mitigates abuse.
- `Revoke` via `JTIUnverified` is intentional (RFC 7009 idempotent revoke); possession of token string is sufficient to revoke that token.
- Residual COSE risk: self-implemented verifier is structurally correct for CIP-8 `Sig_structure`, but lacks production wallet interop evidence.
- Admin `Secure` cookie and owner allowlist (D9) are acceptable per spec; on-chain owner verification remains an operator configuration responsibility.
