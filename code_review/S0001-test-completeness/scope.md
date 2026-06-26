# Review Scope — S0001 测试完备性 (test completeness)

- **Type**: test-completeness audit (NOT a code-change diff). Question: do the unit / integration(PG) / e2e tests cover the production code's critical paths, edge cases, error branches, and concurrency/security invariants — and what is MISSING?
- **Target**: the whole `server/` Go backend at HEAD + its test suite (118 test functions across 34 files).
- **Spec standard**: `docs/specs/20260623T0041-S0001-poolops-issuer-backend.md` (active) — TC-1…TC-26.

## Reviewers
- **cursor-agent** (auto) — `cursor.md`
- **Claude subagent** (general-purpose) — `claude.md`
- **codex** — SKIPPED (unavailable)
- Primary Claude re-verifies findings against real code → `summary.md`

## Test layers under audit
- **Unit** — per-package `*_test.go` (SQLite temp file per test, mock chain, in-memory transports).
- **Integration (PG)** — `internal/inttest/pg_concurrency_test.go` (`//go:build integration`): 24-goroutine concurrent redemption of nonce/auth-code/activation-code/refresh-grant → exactly-one-wins; OAuthClient dialect round-trip. Validated on real PG.
- **E2E** — `internal/e2e/e2e_test.go`: 6 flows through the fully-assembled `NewRouter` over httptest (confidential auth-code lifecycle, public PKCE+device-PoP, blacklist/revoke cascade, activation→Telegram processor→subscription→push, key-rotation JWKS overlap).

## Coverage (measured)
- Per-package (default tags): config 97%, rules 89%, middleware 89%, admin 85.5%, chain 83%, oauth 82%, walletauth 81%, reconciliation 77%, keys 74.5%, jose 72.5%, store 69%, crypto 67.8%, telegram 61.5%, push 61%, httpapi 59%, cmd/issuer 24%, domain 0%, respond 0%.
- **With `-coverpkg=./...` (e2e cross-package execution counted): total 73.6%.** Note: per-package numbers UNDERSTATE reality because e2e (separate package) exercises handlers not credited to httpapi's own number. Use the `-coverpkg` profile as the source of truth for "is this executed at all".
- Artifacts (read these): `tmp/review/S0001-test-completeness/coverage-by-func.txt` (per-package func coverage) and `coverage-by-func-coverpkg.txt` (true coverage incl. e2e).

## Genuinely untested functions (0% even with e2e counted) — prime suspects
- `core/keys/keys.go:Revoke` — signing-key revocation (security; never exercised).
- `worker/push/worker.go:NewWorker/Run` — the push worker POLLING LOOP (the p12-4 fix); e2e tests the Scheduler directly but not the Worker draining `ListScheduled`.
- `httpapi/handlers_admin_resources.go`: `adminCancelSub` (a MUTATION), `adminSubscriptions`, `adminListRules`, `adminListPushJobs`, `adminListClients`, `adminAudit` — admin read/cancel endpoints.
- `store/repo_pushjob.go:ListScheduled/ListByPool`, `store/repo_oauthclient.go:List` (List is only hit by the integration-tagged dialect test).
- `utils/crypto/random.go`: `RandomID/RandomToken/HashToken` — 0% in their own package (used everywhere but no direct unit test of the primitives).
- Integration-only by design (acceptable 0% in unit): `telegram/transport_botapi.go`, `chain/db_sync.go`, `chain/node_lsq.go:execCLI`, `chain/koios.go` HTTP paths, `cmd/issuer` serve/shutdown loop.

## Security/correctness invariants that MUST have tests (assess depth)
COSE/CIP-8 verify cannot be bypassed (wrong key / tampered sig / wrong payload / wrong purpose / alg); nonce replay one-time; PKCE S256 mismatch rejected; refresh rotation + theft-replay revoke-chain; **CAS double-spend exactly-once under PG concurrency** (P0 fix); blacklist gates authorize/refresh/activation; admin RBAC denials + step-up required on rotate/revoke/register; pseudonymous `sub` derivation; AES-GCM field cipher round-trip + tamper; JWKS no private material; introspect/revoke; reconciliation downgrade/expire + fault-isolation; idempotency replay; rate-limit; error envelopes don't leak internals.

## 评审重点 (focus)
找 **缺失的测试**:(1) 0%/低覆盖的关键函数;(2) 已测函数里**未覆盖的错误分支/边界**(过期、重复、非法枚举、nil、并发);(3) 安全不变量是否有**否定用例**(攻击者视角:篡改、重放、越权、伪造);(4) e2e 是否漏了关键链路(如 public-client 的 introspect、step-up 失败、reconciliation 经由 Run 触发);(5) 集成层是否该补(全 store 仓库在 PG 的方言往返、迁移在 PG 应用)。
