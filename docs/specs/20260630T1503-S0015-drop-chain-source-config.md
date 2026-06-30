# Drop chain-source config — Koios is the single origin

Spec-ID: S0015
Status: active
Created Time: 2026-06-30T14:32:11+08:00
Start Time: 2026-06-30T15:03:58+08:00
Completion Time:
Previous Spec-ID: S0014
Closure Reason:

## 1. Requirement Details

### Background

`OUROPASS_CHAIN_KIND` selects the chain data-source adapter (mock | koios | node_lsq |
db_sync | blockfrost). A design review concluded it is largely redundant:

- The eligibility path is already a **read-through cache** (`membership.CachedSource`) over a
  single origin: `active` snapshots are cached per-epoch; `pending`/`none` always hit the
  origin. So the origin (today: koios) is essential, and `chain_kind` merely picks *which*
  origin refills the cache.
- The **sovereignty** justification for `chain_kind` (run your own node instead of trusting a
  third party) is now covered by pointing the **Koios endpoint** at a self-hosted Koios
  instance — i.e. it collapses into "which endpoint", not "which adapter".
- In practice only `koios` and `mock` matter: `blockfrost` is documented but absent from
  `chain.NewSource`'s switch; `db_sync` is a stub (`ErrNotImplemented`); `node_lsq` (direct
  Local State Query) can't even enumerate delegators and is inferior to a self-hosted Koios.
- The config **default is `mock`** (`config.go`), a production footgun: a deploy that forgets
  `OUROPASS_CHAIN_KIND` silently mocks the chain → everyone `not_eligible` (hit during S0014
  on-server debugging).

Operator decision: **standardize on the Koios protocol as the single chain origin** and remove
all chain-source env configuration. mock stays as a **test-only** Go type (injected directly,
never selected via env). Self-hosted Koios, if ever needed, is a **future admin-UI** setting
(per the [[installer-scope-boundary]] principle: operational/admin config does not belong in
deploy-time env). Direct `node_lsq`/`db_sync`/`blockfrost` adapters are **deleted**.

### Scope

- Remove `OUROPASS_CHAIN_KIND` and `OUROPASS_KOIOS_BASE_URL` + `OUROPASS_KOIOS_BASE_URL_<NET>`
  (the latter added in S0014 p1-1) from config/env/installer/docs.
- The issuer **always** builds `CachedSource(KoiosSource(per-network public default))`; the
  per-network public defaults (`chain.DefaultKoiosBaseURL`, from S0014) are the only endpoint source.
- Keep `chain.MockSource` as a test double; remove it from any env/config selection. Add a
  source-injection seam to `buildServices` so tests wire a mock directly. `make dev` uses
  public koios.
- Delete the `node_lsq` / `db_sync` adapters and the `blockfrost`/kind dispatch
  (`chain.NewSource`'s kind switch goes away; `srcFor` builds Koios directly).

### Constraints

- No change to eligibility semantics or the cache layer — only how the origin is constructed.
- Tests keep deterministic data via the injected `MockSource` seam (not via env).
- `OUROPASS_CHAIN_API_KEY` (koios tier / future blockfrost) — keep as the only optional chain
  env (it's a credential, not a source-selector); re-evaluate if unused.
- Server changes covered by `make test` + `pnpm test`; installer `shellcheck`-clean.

### Non-goals

- Admin-UI configuration of a self-hosted Koios endpoint (future spec; public defaults only here).
- Blockfrost support (removed, not reintroduced).
- Reworking `CachedSource` behavior.

## 2. Outline Design

- `config.go`: remove `ChainKind`, `KoiosBaseURL`, `KoiosBaseURLByNetwork`, and their env
  reads + deprecation warnings (the vars simply no longer exist). Keep `ChainAPIKey` (optional).
- `internal/utils/chain`: delete `node_lsq.go` + `db_sync` handling; remove `chain.NewSource`'s
  kind switch (or reduce `Config`/`NewSource` to a Koios constructor). `KoiosSource`,
  `MockSource`, `DefaultKoiosBaseURL`, `CanonicalPoolID` stay.
- `cmd/issuer/main.go`: `srcFor(network)` builds `KoiosSource(DefaultKoiosBaseURL(network), apiKey, network)`
  directly (no kind). `buildServices` gains an optional `chainOverride chain.Source` seam
  (nil → koios) so tests inject `MockSource`.
- Tests (`main_test.go`, `e2e_test.go`, etc.): use the injection seam / direct `MockSource`
  instead of `ChainKind:"mock"`.
- `server/Makefile` `dev`: drop the `OUROPASS_CHAIN_KIND=mock` injection → public koios.
- `deploy/install.sh` / `.env.example` / `docs/deployment.md`: drop the `CHAIN_KIND` prompt and
  the chain-source / koios-URL knobs; document "Koios (public per-network defaults) is the
  source; self-hosting is a future admin-UI option".

### Risk and rollback

- Risk: removing the mock env kind could break tests that boot the full service with mock.
  Mitigation: the `buildServices` injection seam keeps deterministic tests. Rollback = git revert.
- Risk: an existing `.env` with `OUROPASS_CHAIN_KIND`/`OUROPASS_KOIOS_BASE_URL*` — now simply
  ignored (unknown env). Optionally log a one-line deprecation note on boot.

## References

- S0014 (completed) — added per-network koios endpoints (`DefaultKoiosBaseURL`, the `_<NET>`
  overrides this spec removes) and the per-attestor network model.
- `internal/core/membership/cachedsource.go` (read-through cache), `internal/utils/chain/*`.
- `internal/config/config.go`, `cmd/issuer/main.go`.
- Memory: [[installer-scope-boundary]].

## 3. Execution Plan

- [ ] p1-1 Remove chain-source env from config: delete `ChainKind`, `KoiosBaseURL`,
      `KoiosBaseURLByNetwork` + their reads/deprecation; keep `ChainAPIKey`. Update config_test.
- [x] p1-2 `srcFor`/origin always Koios: build `KoiosSource(DefaultKoiosBaseURL(network), …)`
      directly; remove `chain.NewSource` kind switch. Add a `buildServices` source-injection
      seam; migrate `main_test`/`e2e` and other tests to inject `MockSource`.
- [ ] p1-3 Delete `node_lsq` + `db_sync` (+ blockfrost dispatch) adapters and their wiring;
      keep `KoiosSource`/`MockSource`/`DefaultKoiosBaseURL`/`CanonicalPoolID`.
- [ ] p1-4 `make dev`: drop the mock injection → public koios; update the dev docs/comment.
- [ ] p1-5 Installer/docs cleanup: remove the `CHAIN_KIND` prompt + chain-source/koios knobs
      from `install.sh`, `.env.example`, `docs/deployment.md`; document Koios-only + future
      self-host-in-UI.
- [ ] p2-1 Full validation: `make test` + `pnpm test` + `shellcheck deploy/install.sh`.

## 4. Test and Acceptance Criteria

- TC-1 No chain-source env: config has no `ChainKind`/`KoiosBaseURL*`; a fresh `Load()` builds
  with the public Koios origin per network and no chain prompt in the installer.
- TC-2 Koios is the origin: `srcFor(network)` yields `CachedSource(KoiosSource(public default))`
  for mainnet/preprod/preview; no "kind" selection remains.
- TC-3 Test seam: tests inject `MockSource` via `buildServices` (no env); existing
  mock-backed tests (main/e2e) pass unchanged in behavior.
- TC-4 Removed adapters: `node_lsq`/`db_sync`/`blockfrost` are gone; building the issuer with a
  legacy `OUROPASS_CHAIN_KIND` set has no effect (var ignored, optional deprecation log).
- TC-5 Regression: `make test` + `pnpm test` green; `shellcheck deploy/install.sh` clean.

Pass/fail: TC-1..TC-5 pass; eligibility behavior unchanged (no membership semantics drift).

## 5. Execution Log (append-only)

- 2026-06-30T14:32:11+08:00 spec drafted (S0015) after the chain-source design review. Koios
  becomes the single origin; chain-source env removed; mock test-only; node_lsq/db_sync/
  blockfrost deleted; self-hosted koios deferred to a future admin-UI spec. Awaiting review
  before promotion to active.
- 2026-06-30T15:03:58+08:00 promoted to active (Start Time set; file moved to docs/specs/).
  Beginning execution.
- 2026-06-30T15:12:00+08:00 p1-2 done first (ahead of p1-1) to keep every commit buildable:
  removing the config fields (p1-1) would break main.go's compile while it still references
  them, so srcFor must stop referencing them first. main.go: srcFor now builds
  KoiosSource(DefaultKoiosBaseURL(network), ChainAPIKey, network) directly (no kind switch,
  no per-network URL override); buildServices gains a `chainOverride chain.Source` seam
  (nil → koios); run() passes nil. main_test migrated to inject chain.NewMockSource(0) via the
  seam (no ChainKind); db_sync fail-fast subtest removed. e2e_test already injects MockSource
  directly — unchanged. chain.NewSource/Config still exist (removed in p1-3).

## 6. Validation Evidence (append-only)
- TC-3 | stack: go | command: go test ./cmd/issuer/ | result: pass | note: buildServices wires injected MockSource (mock+cache); full+degraded paths green via seam
- TC-2 | stack: go | command: go build ./... | result: pass | note: srcFor builds Koios per-network directly; no kind selection

## 7. Change Requests (append-only)
