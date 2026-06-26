# Claude Review — S0001-test-completeness

Scope reviewed: test-completeness audit (unit + integration + e2e)
Coverage: 73.6% total (with e2e); per-package + by-func in the artifacts
Spec standard: docs/specs/20260623T0041-S0001-poolops-issuer-backend.md

## Assessment
Overall: **NEEDS_MORE_TESTS**

The security core — COSE verify (tamper/wrong-key/wrong-purpose/strict-alg), nonce/auth-code/activation one-time CAS, refresh rotation + theft-chain revoke, PKCE, device-PoP, blacklist gating, admin RBAC + step-up, JWKS no-private-material, error-envelope non-leak, PG concurrency exactly-once — is genuinely and adversarially well tested. The gaps are concentrated in (1) the push **Worker poll loop** (the p12-4 runtime driver, 0% — only the Scheduler half is tested), (2) signing-key **Revoke** (0%, a security op), (3) several admin read endpoints + `adminCancelSub` mutation (0%), and (4) a handful of claimed-but-unproven spec items (TC-3 golden vector, TC-18 trusted-proxy, TC-19 idempotency hardening sub-cases, TC-21 jti-oracle at the HTTP layer). None of the P1s are "feature broken today", but each is a silent-regression hole on a path the spec asserts is covered.

## Findings

### P0 — Critical test gaps
_None._ Every P0-class invariant from the spec (one-time CAS double-spend, refresh theft-chain, COSE non-bypass, step-up on high-blast-radius ops) has at least one real negative test, and the PG concurrency suite (`internal/inttest/pg_concurrency_test.go:95-175`) proves exactly-once under genuine 24-goroutine contention with a sound `start.Wait()` line-up barrier and the correct `won==1` assertion.

### P1 — High

- **`worker/push/worker.go:31` `Worker.Run` + `store/repo_pushjob.go:87` `ListScheduled` (both 0%)** — the push worker poll loop is entirely untested
  - Untested: the runtime driver added by p12-4 — drain `ListScheduled(now, batch)` → `Scheduler.Run` per job → loop. `ListScheduled`'s due-filter (`status='scheduled' AND (scheduled_at IS NULL OR scheduled_at <= now)`, oldest-first) is never executed by any test; e2e Flow D calls `Scheduler.Run` directly on a hand-built job, bypassing the worker and the query. TC-16 claims "push worker 拾取 scheduled job 并投递" but no Go test drives `Worker.Run`.
  - Risk: a regression in the SQL predicate (e.g. wrong status string, a future-`scheduled_at` job picked up early, or the `ScheduledAt IS NULL` branch dropped) or in the poll loop (jobs listed but never run, `ctx.Err()` mid-batch mishandled) ships silently — admin-created push jobs would never deliver and nothing would fail.
  - Suggested test: in `push_test.go`, seed two `PushJob{Status:PushScheduled}` (one `ScheduledAt=nil`, one `ScheduledAt=past`) plus one `Status:PushDone` and one with `ScheduledAt=future`; build `NewWorker(st, capturingSender, 1ms, opts)`, run `Run` under a 100ms `context.WithTimeout`; assert exactly the two due jobs transitioned to `PushDone` and the future/done jobs were untouched. Add a sibling `ListScheduled` unit test asserting the due-filter + ordering directly.

- **`core/keys/keys.go:141` `Revoke` (0%)** — emergency signing-key revocation has no test
  - Untested: `Revoke(kid)` sets `KeyRevoked` + `revoked_at`. No test calls it, and no test asserts a revoked key is dropped from `PublicJWKSKeys` (which only publishes `active`+`rotating`, so revoked should disappear) nor that tokens signed by it stop verifying.
  - Risk: a security primitive ("emergency compromise response, §3.5"). If `SetStatus`/status filtering regressed so a revoked key kept publishing in JWKS, a compromised key's tokens would still verify and nothing would catch it.
  - Suggested test: in `keys_test.go`, `Rotate` twice (active k2 + rotating k1), `Revoke(k1)`, assert `PublicJWKSKeys` no longer contains k1 and contains only k2; `Revoke(k2)`, assert `ActiveSigner` then errors (no active key) and JWKS is empty.

- **`httpapi/handlers_admin_resources.go:139` `adminCancelSub` (0%)** — an admin **mutation** is untested
  - Untested: `POST /subscriptions/{id}/cancel` → `Subscriptions().SetStatus(id, SubCancelled)` + audit. Operator-gated mutation, no test exercises it (or its RBAC: viewer must get 403).
  - Risk: a wiring/role regression (wrong status, missing audit, viewer allowed to cancel) ships unnoticed; subscription cancellation is a member-facing state change.
  - Suggested test: in `handlers_admin_resources_test.go`, operator env, seed an active `SubscriptionSession`, `POST .../subscriptions/{id}/cancel` → 200; assert `GetByChannelUser` status == `SubCancelled` and an `subscription.cancel` audit row exists; viewer env → 403.

- **TC-3 / COSE golden vector — `utils/crypto/cose.go` `Verify` against a real CIP-30 wallet sample (no test)**
  - Untested: every COSE test (`crypto_test.go`, `walletauth_test.go`, e2e) builds the `Sig_structure` with the *same* in-house `cbor.Marshal` the production verifier uses, then verifies it. This is a round-trip self-consistency test, not an interop test — a bug in the `Sig_structure` field order/encoding would be invisible because both sides share it. TC-3 explicitly asks for "真实钱包签名 golden vector"; p12-12 was deferred pending exactly this.
  - Risk: the highest-risk primitive in the system (self-implemented CIP-8, R1 in the spec). A CBOR-shape mismatch with real Nami/Eternl/Lace `signData` output would reject all real wallets in production while every test stays green.
  - Suggested test: capture one real CIP-30 `signData` COSE_Sign1 hex (key, payload, signature) as a frozen byte literal and assert `ParseCOSESign1(...).Verify(pub, payload) == nil`; add a tampered-payload variant of the same fixture asserting `ErrCOSEPayload`/`ErrCOSESignature`.

### P2 — Medium

- **`httpapi/handlers_verifier_introspect.go:14-29` introspect jti-oracle defense (D16/p12-9) — not tested at the HTTP layer**
  - Untested: the handler hardcodes the jti arg to `""` so a client-supplied `{"jti":...}` is ignored — the whole point of p12-9. The only test that passes a bare jti is `introspect_test.go:53`, which calls the *core* `Introspect(ctx, "", "no-such-jti")` directly (bypassing the handler that strips it). TC-21 claims "未鉴权裸 jti introspect 不再返回 active 状态" but no test posts a jti to `/api/oauth/introspect`.
  - Risk: if someone re-wired `parseTokenBody(r).jti` into the `Introspect` call, the endpoint becomes a token-status oracle for jti enumeration and no test would fail.
  - Suggested test: mint a token (so its jti is active in the ledger), then `POST /api/oauth/introspect {"jti":"<that active jti>"}` and assert `active=false` (the supplied jti must be ignored); also assert a valid `{"token":...}` still returns `active=true`.

- **TC-18 trusted-proxy IP resolution (`middleware.go:120` `clientIP`, p12-6) — no test**
  - Untested: the X-Forwarded-File handling is the security control (default false → ignore XFF, take RemoteAddr; trusted → rightmost non-trusted hop). `clientIP` is at 75%; no test sets `X-Forwarded-For` and asserts it's ignored by default vs honored when trusted. TC-18 asserts both branches.
  - Risk: a spoofable client IP feeds rate-limiting and audit logs; a regression to "always trust XFF" would let an attacker evade per-IP rate limits and forge audit source IPs, silently.
  - Suggested test: table test on the middleware — request with `X-Forwarded-For: 1.2.3.4` and `RemoteAddr: 10.0.0.9:1`: default config → resolved IP is `10.0.0.9`; trusted-proxy config → `1.2.3.4` (or rightmost non-trusted). Assert the rate-limiter buckets accordingly.

- **TC-19 idempotency hardening sub-cases (`middleware.go:150-208`, p12-7) — partially tested**
  - Untested: `middleware_test.go:44` proves replay + per-key isolation, but NOT the two hardening invariants p12-7 added: (a) non-2xx responses are NOT cached (a 500 must be retryable), and (b) the key is namespaced by `Method+Path` so the same Idempotency-Key on a different endpoint doesn't cross-replay. TC-19 lists both.
  - Risk: failure-poisoning (a cached 500 wedging a client) or cross-endpoint response bleed regresses unnoticed.
  - Suggested test: handler that returns 500 on first call, 200 on second → same key, assert the second call actually re-runs (not a replayed 500). Second test: same key, two different paths → two distinct responses, `Idempotency-Replayed` not set on the second path.

- **`worker/push/push.go:129` `deliver` — rate-limit/backoff timing and per-recipient limiting not asserted**
  - Untested: tests use `RatePerSec:100000` to disable the limiter, so the p12-4 "限流按收件人计" (rate-limit counted per recipient) and backoff behavior are never exercised; `TestDeliver_RetriesThenSucceeds` asserts the final count but not that backoff sleeps occurred or that the limiter gates throughput.
  - Risk: a regression dropping the per-recipient limiter (e.g. flooding Telegram and getting the bot rate-limited/banned) passes all current tests.
  - Suggested test: low `RatePerSec` (e.g. 5) + burst 1 with several recipients and a fake clock or elapsed-time assertion; assert sends are paced, not all-at-once.

- **Reconciliation upgrade direction (`reconciliation.go:78`) — only downgrade is asserted**
  - Untested: `TestReconcile_DowngradeExpireKeep` covers gold→silver and gold→expired, but the tier-change branch is symmetric (silver→gold upgrade with refreshed entitlements). The comment says "downgrade/upgrade" but no test drives an upgrade.
  - Risk: low, but an asymmetric bug (e.g. only applying the tier change when new < old) would let upgrades silently fail.
  - Suggested test: seed a `silver` session, program elig to return `gold`, assert `Downgraded==1` and the persisted session is `gold` with the gold entitlements.

### P3 — Low

- **`utils/crypto/random.go` `RandomID/RandomToken/HashToken` (100% via callers, no direct unit test)** — acceptable. These are exercised constantly through OAuth/keys; a dedicated test (HashToken determinism, RandomToken length/uniqueness, non-empty) would be nice-to-have but not load-bearing.
- **`adminSubscriptions`/`adminListRules`/`adminListPushJobs`/`adminListClients`/`adminAudit` (all 0%)** — read-only admin list endpoints. `adminListClients` is worth a small test because it strips `ClientSecretHash` before responding (`handlers_admin_resources.go:275`) — a regression there would leak secret hashes; assert the field is absent in the JSON. The others are low-value list passthroughs; flag, don't pad.
- **Integration-only adapters (`telegram/transport_botapi.go`, `chain/db_sync.go`, `chain/node_lsq.go:execCLI`, `chain/koios.go` HTTP, `cmd/issuer` serve loop, all 0%)** — acceptable gaps by design (external process / network); the spec scope explicitly excuses these.

## Spec Compliance (TC coverage)

- TC-1 服务启动/优雅退出: **met** — `cmd/issuer/main_test.go:29` (buildServices full/degraded), `:75` (runNonceGC stops on cancel); e2e drives the assembled router. WaitGroup join itself only smoke-tested (TC-22 note), not a Go test.
- TC-2 双栈持久层: **met** — SQLite throughout unit store tests; PG dialect round-trip + migrations apply on PG via `inttest/pg_concurrency_test.go:181` (`TestPG_DialectRoundTrip`). PG run evidenced 5/5 in spec §6.
- TC-3 CIP-30 golden vector: **partially-met (weaker than claimed)** — COSE tamper/key/alg/purpose all tested (`crypto_test.go:101,143`), but every fixture is self-generated with the production CBOR path; no real-wallet golden vector (p12-12 deferred). See P1.
- TC-4 JOSE/JWKS: **met** — `jose_test.go:13` (independent verify, kid/typ, no cert chain), `:76` (OKP-only, no `d`/x5c). `handlers_verifier_test.go:73` re-checks no private leak at the endpoint.
- TC-5 资格引擎: **met** — `rules/engine_test.go` covers priority, big.Int thresholds, grace, fail-closed (min_epochs, required_status), determinism, disabled-rule. Strong table test.
- TC-6 授权码流: **met** — `oauth_test.go` (eligible/ineligible/blacklist/client-validation), `handlers_oauth_test.go` (302+code, not_eligible redirect), e2e Flow A.
- TC-7 刷新与盗用检测: **met** — `refresh_test.go:55` (replay revokes chain, descendant revoked), `:81` (re-eval downgrade/deny), `token_test.go:191` (code reuse); e2e Flow A replays old refresh → 400.
- TC-8 密钥轮换/吊销: **partially-met** — rotation+JWKS overlap fully tested (`keys_test.go:32`, e2e Flow F); revoke-after introspect-inactive tested (`introspect_test.go:40`, e2e). But signing-key `Revoke` (keys.go:141) — the §3.5 emergency path — is 0%. See P1.
- TC-9 渠道激活 + Telegram: **met** — `telegram_test.go` (bind/reuse/invalid/ineligible/status/unsubscribe/worker dispatch), e2e Flow D (activation HTTP → processor → session, consumed-code no second session).
- TC-10 推送过滤/限速/退避/DeliveryLog: **partially-met** — tier/topic/entitlement filtering + retry/exhaust + DeliveryLog counts tested (`push_test.go`), but the limiter is disabled in all tests (RatePerSec=100000); pacing/backoff timing and per-recipient limiting unasserted. See P2.
- TC-11 Reconciliation: **met** — `reconciliation_test.go:54` (downgrade/expire/keep + counts), `:107` (fault isolation), `:150` (Run triggers on epoch advance). Upgrade direction untested (P2).
- TC-12 Admin RBAC/step-up/audit: **met** — `admin_test.go` (owner bootstrap, non-owner forbidden, AtLeast, step-up wrong-key), `handlers_admin_test.go` (cookie flow, unauth 401), `handlers_admin_resources_test.go:72` (RBAC matrix), `:87` (revoke needs step-up + cascade), `:199` (register/rotate need step-up + audit).
- TC-13 一次性 Consume CAS: **met** — `repo_tokens_test.go:175` (3-table guarded UPDATE first=1/second=0), and `inttest:95-152` proves real concurrent exactly-once on PG.
- TC-14 refresh CAS+原子: **met** — `repo_tokens_test.go:71` (RotateIfActive active=1/rotated=0/unknown=0), `inttest:154` (concurrent rotate exactly-once). Note: "mint fails → grant not left rotated" (the WithTx rollback) is asserted only indirectly; the rollback-on-mint-error path itself has no direct test (acceptable — covered by the atomic happy path + CAS).
- TC-15 reconciler 隔离: **met** — `reconciliation_test.go:107` (Failed=1, others applied, bad left active).
- TC-16 worker 生命周期: **partially-met (weaker than claimed)** — telegram worker dispatch tested (`telegram_test.go:148`); push **Worker.Run + ListScheduled are 0%** — only the Scheduler is driven. WaitGroup join is smoke-test-only. See P1.
- TC-17 public PoP: **met** — `token_test.go:134` (malformed device→invalid_request, bound refresh needs matching device), e2e Flow B.
- TC-18 可信 IP: **not-met** — no test sets X-Forwarded-For or exercises trusted vs untrusted resolution; `clientIP` 75%. TC-18 evidence in spec only says "package green, existing assertions unchanged". See P2.
- TC-19 幂等加固: **partially-met** — replay + per-key isolation tested (`middleware_test.go:44`); the two p12-7 hardening invariants (non-2xx not cached; Method+Path namespacing) are not. See P2.
- TC-20 链身份/fail-closed: **met** — `engine_test.go:70,87` (min_epochs + required_status fail closed), bech32 vector (`stakeaddr_test.go`, per spec §6).
- TC-21 verifier/admin 加固: **partially-met** — error-envelope non-leak (`handlers_admin_resources_test.go:157`) and step-up alignment tested; but the jti-oracle removal is tested only at the core (jti stripped at the *handler* is unverified), and signing-face 429 rate-limit (`publicLimit` on authorize/token/activation) has no test — only the standalone middleware 429 (`middleware_test.go:30`) exists. See P2.
- TC-22 低危批: **partially-met** — constant-time compare exercised via wrong-secret paths; empty POOL_ID fail-fast and db_sync fail-fast tested (`main_test.go:55,67`); strict-alg COSE tested (`crypto_test.go:143`). Telegram non-message-skip and `From.ID==0` skip not unit-tested (transport is integration-only — acceptable).
- TC-23 单元补齐: **met** — `config_test.go` (validate table test, per spec), `main_test.go` (buildServices, worker assembly), httpapi negative paths present.
- TC-24 e2e: **met** — `e2e_test.go` 6 flows over `NewRouter` (note: spec text says 5; the file has 6 incl. key rotation). Caveat: Flow E "reconciliation" is asserted via the reconciler unit, not driven through the router's Run loop end-to-end.
- TC-25 PG 集成: **met** — `inttest/pg_concurrency_test.go`; design is sound (real contention barrier, correct exactly-once assertions); spec §6 evidences pass on real PG ×2.
- TC-26 测试编排: **met (out of code scope)** — Makefile + CI per spec §6; not re-verifiable here but the build-tag isolation (`//go:build integration`, separate `inttest` package) is correct so `go test ./...` stays Docker-free.

## Test quality notes
- **Self-referential COSE/JOSE fixtures**: COSE and JWS tests generate signatures with the same code paths they verify. Correct for round-trip, but they cannot catch an interop/encoding bug — only a frozen external golden vector can (TC-3). This is the single most important quality gap given CIP-8 is self-implemented.
- **Rate limiter disabled in push tests**: `RatePerSec:100000` makes the limiter a no-op, so all push assertions are about filtering/retry, never throughput — TC-10's "限速" clause is unproven.
- **Scheduler-not-Worker in e2e**: Flow D and all push tests call `Scheduler.Run(job)` directly; the worker poll/dispatch layer is invisible to the suite. Good separation, but leaves the runtime driver (the actual p12-4 deliverable) untested.
- **No tautologies or shared-state flakiness found**: each test uses a fresh `t.TempDir()` SQLite file; the PG `inttest` uses per-run unique keys (`uk()`) so it's safe against a shared DSN; assertions check state (DB rows, statuses, body contents), not just status codes. `TestServerError_GenericNoLeak` is an exemplary non-leak assertion.
- **Time assumptions**: a few `time.Sleep(2*time.Millisecond)` in `keys_test.go` to force distinct kids and `context.WithTimeout(200ms)` worker tests — tolerant enough, low flake risk, acceptable.

## Strengths (well covered)
- **Adversarial OAuth core**: code reuse, refresh replay → chain revoke (incl. descendant), wrong client_secret (constant-time), wrong PKCE verifier, device-PoP mismatch, ineligible/blacklisted at both authorize and refresh — all have explicit negative tests.
- **One-time-use under real concurrency**: the PG `inttest` suite is the highlight — it genuinely contends 24 goroutines and asserts exactly-one for nonce/auth-code/activation/refresh, which SQLite's serialized writer cannot prove. Sound design and assertions.
- **Crypto invariants**: COSE tamper/wrong-key/wrong-alg/strict-alg, field-cipher round-trip + tamper + bad-key-size, blake2b known-vector, sub determinism + salt sensitivity, JWKS no-private-material — thorough.
- **Admin security matrix**: RBAC denials (viewer/operator/owner), step-up required + wrong-key rejection on revoke/register/rotate, audit-row assertions, and a dedicated error-envelope no-leak test.
- **Error-branch discipline in the store**: `Consume` paths assert the full sentinel set (ErrConsumed/ErrNotFound/ErrPurpose/ErrExpired) and the CAS guard at the SQL level.
