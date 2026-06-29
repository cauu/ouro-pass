# Per-attestor network model + config robustness + binding fixes

Spec-ID: S0014
Status: active
Created Time: 2026-06-29T23:01:36+08:00
Start Time: 2026-06-29T23:47:17+08:00
Completion Time:
Previous Spec-ID: S0013
Closure Reason:

## 1. Requirement Details

### Background

Live Telegram subscription-binding testing on the S0013 mainnet deployment exposed a
cluster of issues. The deepest is a **network-model contradiction**: since S0006 the
network is a **per-attestor** property, yet several *global* knobs cut across that model and
caused every footgun in this session:

- `OUROPASS_NETWORK` (global) — drives the server-rendered bind/connect page wallet guard
  (`router.go` `Deps.Network`), the default network for new attestors, the reconciler +
  admin-delegator "default-network source", an admin `/pool` response field, and a startup
  log.
- `OUROPASS_KOIOS_BASE_URL` (single, global) — one koios endpoint applied to **all**
  networks, though koios endpoints are per-network (api/preprod/preview.koios.rest).
- **Two** wallet network guards reject a wallet whose `getNetworkId()` ≠ the configured
  network: the member-facing authpage JS (`authpage/assets/ouropass-auth.js`, fed by
  `OUROPASS_NETWORK`) **and** the admin SPA (`web/src/wallet/adapter.ts`, fed by the
  build-time `VITE_ISSUER_NETWORK`).

Symptoms: a mainnet wallet was rejected at the bind page ("wrong network") because global
`OUROPASS_NETWORK` wasn't mainnet; earlier "not eligible" because a per-attestor network ≠
the global koios URL's network derives a wrong-network stake address that queries the wrong
koios.

Decision (operator): **network is purely an attestor property; there is no "issuer
network".** The credential hash is network-agnostic — network only selects which chain to
query, an attestor concern, not a deploy-time one. This satisfies the
[[installer-scope-boundary]] principle: network belongs to an admin-managed DB entity
(attestor), so it must not be an installer prompt — the same test that removed Telegram from
the installer in S0013. Testnet networks stay **visible/selectable** in admin (legit preprod
SPOs exist); the attestor form simply **defaults to `mainnet`**.

Smaller issues found alongside (kept here): koios base URL used verbatim (a trailing slash →
`//account_info` → koios 404); installer DOMAIN input unsanitized; Telegram `getUpdates` 409
floods the logs; pool-ID comparison may be format-sensitive (bech32 vs hex).

### Scope

1. **Per-attestor network model** — remove the global-network coupling end to end (server,
   both wallet guards, reconciler, admin delegators, admin `/pool`, installer, dev tooling).
2. **Per-network chain endpoint** — koios endpoint resolved per network (defaults + optional
   per-network overrides).
3. **Config-input robustness** — koios base URL trim; installer DOMAIN sanitize.
4. **Telegram worker resilience** — `getUpdates` 409 no longer floods logs.
5. **Eligibility correctness** — pool-ID comparison is format-agnostic + clearer diagnostics.

### Constraints

- Network-agnostic by design: a wallet on any network may attempt; eligibility is decided by
  evaluating the credential against attestor rules, never by the wallet's self-reported
  network. No change to *who* qualifies on a given network.
- Security unchanged: `sub`/credential hash and signature verification are already
  network-agnostic; dropping both wallet guards introduces no cross-network leakage (a
  testnet key queried on mainnet koios simply has no delegation → not eligible).
- Per-network endpoint resolution applies only to **HTTP chain sources (koios; blockfrost
  same pattern)**; `node_lsq` / `db_sync` / `mock` behavior is unchanged.
- Removed global env (`OUROPASS_NETWORK`, single `OUROPASS_KOIOS_BASE_URL`): if present in an
  upgraded `.env`, log a one-line **deprecation warning** and ignore — never silently change
  meaning.
- `network` becomes a **required, validated** attestor param (no empty fallback to a global).
- Server changes covered by `make test` (Go) + `pnpm test` (web); installer `shellcheck`-clean.

### Non-goals

- Hiding testnet in admin (decision: visible/selectable, default `mainnet`).
- A deploy-time testnet opt-in flag (rejected in favor of the admin-form default).
- Reworking `OUROPASS_CHAIN_KIND` — chain-*access* (infra) choice, stays a deployment
  concern; only the per-network *endpoint* resolution changes.
- **Blockfrost per-network API key** — this spec does koios URL per-network only; blockfrost
  project-id/key stays global for now (documented limitation + follow-up), so a multi-network
  blockfrost deploy is out of scope.
- Destructive installer `--reset`.

## 2. Outline Design

### Network model (core change) — explicit residual cleanup

Source of truth = `AttestorConfig.params.network`. Everything below is **removed or
re-pointed**:

| Location | Now | Change |
|---|---|---|
| `config.go` `defaultNetwork="preview"`, `env("OUROPASS_NETWORK", …)`, validation (:107) | global net + default | remove env + validation; deprecation-warn if present |
| `config_test.go` (:22,:43,:47) | tests OUROPASS_NETWORK | drop/replace those cases |
| `router.go:40` `Deps.Network` (+ comment) | feeds authpage guard | remove field + guard |
| `cmd/issuer/main.go:208` startup log `cfg.Network` | logs a global net | remove |
| `cmd/issuer/main.go:259/285/303` `Deps.Network`, srcFor fallback, `srcFor(cfg.Network)` | global default source | remove fallback; per-network (below) |
| `handlers_admin_resources.go:116` `/pool` returns `h.d.Network` | global net in response | remove (meaningless per-attestor) |
| `handlers_admin_attestors.go` `validateAttestorInput` | allows `network==""` | require + validate network |
| `web/src/wallet/adapter.ts:45-52`, `lib/config.ts` `VITE_ISSUER_NETWORK`, `adapter.test.ts` | SPA wallet guard | remove guard + build env; network-agnostic owner login |
| `authpage/assets/ouropass-auth.js` guard, `bind.html`/`connect.html` `data-network`, `authpage.{Bind,Connect}Data.Network`, `handlers_oauth.go` | authpage guard | remove |
| `web` attestor form (SourcesSection.tsx) | already defaults mainnet ✓ | keep; ensure all networks selectable |
| `deploy/install.sh` NETWORK prompt + `OURO_NETWORK` env + `--help`, `deploy/init.sh` "review OUROPASS_NETWORK" | installer asks net | remove |
| `server/Makefile` `dev` injects `OUROPASS_NETWORK` | dev env | drop (mock kind needs no net) |

### Per-network chain endpoint

- `srcFor(network)` resolves the koios endpoint for *that* network:
  - built-in public defaults — `mainnet=https://api.koios.rest/api/v1`,
    `preprod=https://preprod.koios.rest/api/v1`, `preview=https://preview.koios.rest/api/v1`;
  - optional overrides `OUROPASS_KOIOS_BASE_URL_MAINNET|_PREPROD|_PREVIEW` (self-hosted koios);
  - legacy single `OUROPASS_KOIOS_BASE_URL` → deprecation warning, ignored.
- `node_lsq`/`db_sync`/`mock` unchanged.

### Reconciler + admin delegators per network

The single `chainSrc = srcFor(cfg.Network)` (used by both `reconciliation.New(...)` and
admin `adminDelegators` via `h.d.Chain`) is replaced. **Chosen approach: B** — a single
reconciler that maintains a `map[network]epochCursor`, derives the distinct network set from
active attestors each tick, and ticks/queries each network on its own source+epoch (fewer
goroutines than one-worker-per-network, simpler shutdown). Admin delegator roster routes by
the target pool's attestor `params.network` (not a global source). If a delegators call
can't resolve a network, it errors clearly rather than using the wrong network.

### Pool-ID normalization (single point)

`DeriveState` (`membership/state.go:45-49`) compares pool IDs literally. Add one canonical
normalizer (a helper in the `chain` package, e.g. `CanonicalPoolID`) and apply it at the
`DeriveState` entry to BOTH the snapshot pool IDs and the configured `poolID`, converting
bech32 `pool1...` ↔ hex to one canonical form. Validate/normalize the attestor `pool_id` at
config time too, so admin stores a canonical value. Exact-match semantics preserved.

### Risk and rollback

- Reconciler map[network] change is the riskiest; mitigate with multi-network unit tests
  (two sources, two epochs) and clear delegator-routing errors. Rollback = `git revert` (no
  schema migration; `params.network` already exists).
- Removing both wallet guards is safe (network-agnostic verified); re-test admin owner login
  + bind/connect end to end (TC-4).

## References

- S0013 (completed) — deployment + input-sanitization follow-ups.
- `cmd/issuer/main.go:208/259/285/303`, `internal/config/config.go:58/70/107`.
- `internal/httpapi/router.go:40`, `handlers_admin_resources.go:116`, `handlers_admin_attestors.go`.
- `internal/httpapi/authpage/assets/ouropass-auth.js`, `templates/bind.html`.
- `web/src/wallet/adapter.ts`, `web/src/lib/config.ts` (`VITE_ISSUER_NETWORK`).
- `internal/utils/chain/koios.go`, `internal/core/membership/state.go`, `internal/worker/{telegram,reconciliation}/`.
- Memory: [[installer-scope-boundary]].

## 3. Execution Plan

- [ ] p1-1 Per-network koios endpoint: `srcFor(network)`/`chain.NewSource` resolve the
      endpoint per network (public defaults + `OUROPASS_KOIOS_BASE_URL_<NET>` overrides);
      deprecation-warn on the legacy single var. node_lsq/db_sync/mock untouched. Unit tests.
- [ ] p1-2 Remove global `OUROPASS_NETWORK` everywhere per the residual-cleanup table
      (config + test, router Deps.Network, main log/wiring, admin `/pool`, validateAttestorInput
      now requires network); deprecation-warn if an old `.env` still sets it.
- [ ] p1-3 Reconciler + admin delegators per network (approach B: `map[network]epochCursor`,
      network set derived from active attestors; delegators route by attestor `params.network`,
      error on unresolved). Multi-network unit tests.
- [ ] p1-4 Drop BOTH wallet guards: authpage JS + `data-network` + `*Data.Network`
      (server-fed) and SPA `adapter.ts` + `VITE_ISSUER_NETWORK` (build-fed); owner login +
      bind/connect become network-agnostic; show a clear not-eligible message instead.
- [ ] p1-5 Installer/dev cleanup: remove NETWORK + koios-URL prompts from `install.sh`
      (+ `OURO_NETWORK` env + `--help`), the `init.sh` "review OUROPASS_NETWORK" line, and the
      `Makefile` `dev` `OUROPASS_NETWORK` injection; document per-network koios overrides as
      advanced in `.env.example`/`docs/deployment.md`. Record the behavior change (installer
      previously defaulted `preprod`; now no network prompt, admin defaults `mainnet`).
- [x] p2-1 koios base URL robustness: trim whitespace + trailing `/` in `NewKoiosSource`
      (applies to per-network override values).
- [x] p2-2 Installer DOMAIN sanitization: strip `http(s)://` + trailing `/`.
- [x] p3-1 Telegram `getUpdates` 409: detect, back off, log a single clear diagnostic
      (another poller/webhook owns this token); optional `deleteWebhook` on start.
- [x] p4-1 Pool-ID normalization: single canonical helper applied at `DeriveState` entry to
      both sides + validate/normalize attestor `pool_id` at config time; clear not-eligible
      diagnostic (configured vs on-chain pool). Unit tests for bech32/hex.
- [ ] p5-1 Full validation: `make test` + `pnpm test` + `shellcheck deploy/install.sh` +
      on-server end-to-end mainnet bind.

## 4. Test and Acceptance Criteria

- TC-1 Per-network endpoint: `network=preprod` queries `preprod.koios.rest`, `mainnet`
  queries `api.koios.rest`, regardless of any single global URL; `OUROPASS_KOIOS_BASE_URL_MAINNET`
  override honored; legacy single var logs a deprecation warning.
- TC-2 No global network: a fresh install has no network prompt; `OUROPASS_NETWORK` is not
  read (present-in-`.env` → deprecation warning, ignored); admin attestor form defaults
  `mainnet` with all networks selectable; `validateAttestorInput` rejects an empty/invalid
  network.
- TC-3 Multi-network reconciliation + delegators: with a mainnet and a preprod attestor, each
  is reconciled on its own network's epoch, and the admin delegator roster for each pool uses
  that pool's network source (unit test with two sources).
- TC-4 Both guards gone: a mainnet wallet proceeds past the member bind page AND admin owner
  login with no "wrong network"; a non-matching key gets a clear not-eligible message, not a
  network block; no `VITE_ISSUER_NETWORK` needed at build.
- TC-5 koios URL trim: `https://api.koios.rest/api/v1/ ` (override) issues to
  `.../api/v1/account_info` (single slash).
- TC-6 Installer DOMAIN sanitize: entering `https://pass.example.com/` writes
  `DOMAIN=pass.example.com`.
- TC-7 Telegram 409: a 409-returning transport backs off and logs once per window (no
  per-second flood / busy loop).
- TC-8 Pool-ID format: a delegator to the attestor's pool resolves eligible whether the pool
  id was configured bech32 or hex; a non-delegator stays `StateNone` (no loosening).
- TC-9 Regression: `make test` + `pnpm test` green; `shellcheck deploy/install.sh` clean.
- TC-10 On-server: the qualifying mainnet wallet binds end-to-end (bot replies
  "Subscribed!"); logs readable (no 409 flood).
- TC-11 Upgrade path (manual/doc): a deployment whose `.env` still contains `OUROPASS_NETWORK`
  / `OUROPASS_KOIOS_BASE_URL` starts cleanly, logs the deprecation warning, and behaves
  per-attestor.

Pass/fail: TC-1..TC-11 pass; TC-4 (network-agnostic bind + owner login) and TC-10 (real
mainnet bind) mandatory; TC-8 must keep `StateNone` for non-delegators (no loosening).

## 5. Execution Log (append-only)

- 2026-06-29T23:01:36+08:00 spec drafted (S0014) as config-robustness + binding fixes.
- 2026-06-29T23:30:00+08:00 redrafted around the network-model contradiction; operator
  decision: network is purely an attestor property (admin default `mainnet`, testnet
  visible/selectable); no "issuer network".
- 2026-06-29T23:55:00+08:00 incorporated an external (Cursor) review: verified and added the
  missing items — SPA second wallet guard (`VITE_ISSUER_NETWORK`), reconciler **and** admin
  delegators per-network (approach B), full residual-cleanup table (config/test, router,
  main log, admin `/pool`, validateAttestorInput, init.sh, Makefile), pool-ID single
  normalization point, blockfrost scoped out (koios-only this spec), deprecation handling for
  old env, and TC-11 (upgrade path).
- 2026-06-29T23:47:17+08:00 operator approved the draft + the 3 convergence decisions
  (blockfrost koios-only scope, reconciler approach B, deprecation-warn on old env). Promoted
  draft → active. Execution order: p2/p3 (robustness + 409) → p1 (network model) → p4 → p5.
- 2026-06-29T23:47:17+08:00 p2-1 started: koios base URL trim in NewKoiosSource.
- 2026-06-29T23:47:17+08:00 p2-1 completed: `NewKoiosSource` trims surrounding whitespace + trailing slashes before the empty→default check; added koios_test.go (TestNewKoiosSourceTrimsBaseURL). This is the direct fix for the `//account_info` 404 hit on-server.
- 2026-06-30T00:00:00+08:00 p2-2 completed: installer strips scheme + path/trailing-slash from the DOMAIN answer (`${DOMAIN#http(s)://}`, `${DOMAIN%%/*}`); port preserved. shellcheck clean.
- 2026-06-30T00:10:00+08:00 p4-1 completed: added `chain.CanonicalPoolID` (hex 28-byte → bech32 `pool1…` via the existing BIP-173-vector-tested encoder; bech32 lower-cased/kept) and applied it at the `DeriveState` entry to both the configured poolID and the snapshot pools. Hex-configured pools now match koios's bech32. Tests: poolid_test.go + state_poolid_test.go; existing TestDeriveState (placeholder ids) still green. Note: config-time normalization is unnecessary for correctness once comparison is format-agnostic; the per-attestor not-eligible diagnostic log is deferred (can add a slog.Debug in the attestor Attest path if needed).
- 2026-06-30T00:05:00+08:00 p3-1 completed: telegram worker detects getUpdates 409 (isConflict), backs off 30s (vs 1s transient), and throttles the log (conflict: once + ~5min heartbeat; transient: ~1/30s) with a clear "another poller/webhook owns this token" message. Added backoff_test.go. Deferred (optional in spec): deleteWebhook-on-start — the common cause is a dual poller, not a webhook; can add later if needed.

## 6. Validation Evidence (append-only)

- TC-5 | stack: node/go | command: go test ./internal/utils/chain/ -run TrimsBaseURL | result: pass | note: trailing slash(es) + whitespace trimmed (`…/api/v1/`→`…/api/v1`); empty→mainnet default. go build ./... clean.
- TC-6 | stack: other | command: shellcheck + sanitize harness | result: pass | note: `https://host/`→`host`, `http://host`→`host`, `host/admin/`→`host`, `host:8443` preserved. shellcheck clean.
- TC-7 | stack: go | command: go test ./internal/worker/telegram/ -run 'Conflict|Backoff' | result: pass | note: isConflict detects 409; conflict→30s backoff, transient→1s; Run on a persistent 409 doesn't busy-loop (≤5 polls) and returns promptly on ctx cancel; clear conflict message logged once (failures==1, then throttled).
- TC-8 | stack: go | command: go test ./internal/utils/chain/ ./internal/core/membership/ | result: pass | note: CanonicalPoolID hex↔bech32 equal + shape (pool1…, len 56, BIP-173 encoder); DeriveState eligible for hex-vs-bech32 (active+pending), StateNone for a different pool (no loosening); existing DeriveState test still green.

## 7. Change Requests (append-only)
