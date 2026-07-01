# Staking-identity attestation — chain data architecture (S0004)

> **Superseded in part by S0006.** The single-pool model below is now one *kind*
> of a general **on-chain credential (Attestor)** abstraction — see
> [`onchain-credentials.md`](onchain-credentials.md). The chain data / three-state
> derivation here is unchanged; what changed: the issuer evaluates a *set* of
> attestors (ANY-of gate), the token carries a `credentials[]` array (not flat
> claims), `tier_rules` are issuer-global over aggregate facts, and
> `OUROPASS_POOL_ID` is replaced by attestor config + required `OUROPASS_ISSUER`.

This document describes the issuer's data model after the S0004 redesign: the
issuer is a **staking-identity attestation provider**, not a membership-policy
engine. It proves *facts* about a credential's relationship to the pool; business
policy (thresholds → access) is the **relying party's** to apply. The issuer keeps
only a thin first-party tier opinion for its own channels (Telegram/Push).

## 1. Roles

| Party | Responsibility |
|---|---|
| **issuer** | Interpret on-chain facts → membership **state** + exact **active stake** + **epochs active** + **member_since**. Thin gate: only pool members get tokens. |
| **relying party (RP)** | Read the raw token claims and apply its own policy (thresholds, grace, entitlements). |
| **issuer's own channels** | Use the optional first-party `tier` (from `PoolConfig.tier_rules`) for Telegram membership / Push targeting. |

## 2. Chain data (`internal/utils/chain`)

`Source.Snapshot(sch)` returns raw facts (pool-agnostic — never a conclusion):

| Snapshot field | Koios source | meaning |
|---|---|---|
| `DelegatedPoolID` | `/account_info` `delegated_pool` | **live** delegation — drives `pending` |
| `AccountStatus` | `/account_info` `status` | `registered` gate |
| `ActiveStakePoolID` | `/account_stake_history` latest row `pool_id` | pool the **effective active stake** counts for — drives `active` |
| `ActiveStakeLovelace` | `/account_stake_history` latest `active_stake` | exact active stake (replaces the old `total_balance` approximation) |
| `EpochsDelegated` | trailing consecutive `account_stake_history` rows for `ActiveStakePoolID` | epochs continuously active |

Two delegation signals must not be conflated: **live** delegation (`account_info`)
moves immediately; **active stake** (`account_stake_history`) lags by ~2 epochs.
That lag *is* the leaving tail.

Source fidelity: Koios is the production source. `node_lsq`/`db_sync` provide live
delegation only (no active-stake history) → they can yield `pending`/`none` but
not `active`. `mock` is for tests.

> Endpoint/shape note: `/account_stake_history` and `/pool_delegators` per S0004
> §2.4/§2.7 — confirm against live Koios in integration (R1).

## 3. Three-state machine (`internal/core/membership`, `DeriveState`)

Pure function, classified relative to the issuer's pool:

```
active  iff ActiveStakePoolID == ourPool          // includes the leaving tail
pending iff registered && DelegatedPoolID == ourPool && not active   // entering, ~2 epochs out
none    otherwise
```

- State is **amount-independent** — `active` regardless of stake size. Amount → tier
  is a separate first-party concern.
- **Entering**: live-delegate to us → `pending`; ~2 epochs later active stake lands → `active`.
- **Leaving**: redelegate away → live moves but active stake lingers ~2 epochs →
  still `active`; then the epoch snapshot drops us → `none`. Grace is the RP's.
- Instant cut (risk/ban) is the existing admin revoke (blacklist), independent of chain.

## 4. Active-only cache (`membership.CachedSource`)

Wraps `chain.Source`, implements `chain.Source` (the eligibility path is unchanged).

- **Only `active` is cached** — it derives from epoch-stable active-stake history, so
  it is safe to reuse within an epoch. `pending`/`none` hinge on live delegation and
  are recomputed every call (onboarding and bail are immediate and symmetric).
- **Hit iff** the cached row's epoch == the **locally computed** current epoch
  (`chain.CurrentEpoch`, per-network genesis constants — no chain round-trip, D7).
  A wrong/unknown epoch can never serve a stale hit (the stored epoch won't match);
  it just degrades to always-live.
- `singleflight` collapses a herd on one credential; a context timeout bounds the
  origin call. Failure policy (D8) is the caller's: login/issue → fail-closed;
  reconciler → soft fail-open.
- A bail (active → pending/none) deletes the cache row.

The reconciler (`internal/worker/reconciliation`) re-derives state **and the
first-party tier** (`Attest`) at each epoch boundary and drives the subscription
lifecycle (S0019):

- **member (`active`/`pending`)** → refresh `LastVerifiedAt` + `Tier` (so a
  `pending → active` upgrade or stake-driven tier change is reflected without a
  re-bind), slide the informational `ExpiresAt = now + TTL` (30 days), and clear any
  grace. `ExpiresAt` is a friendly "valid through, auto-renews" date — **never
  enforced**.
- **membership lost (`state == none`)** → the first pass records a grace **deadline**
  `GraceUntil = now + GRACE` (5 days ≈ 1 mainnet epoch) and DMs the user once; the
  session stays active. Terminal expiry is `now >= GraceUntil` (a pure timestamp — the
  reconcile frequency only affects detection latency). Recovery to member before the
  deadline restores the session (grace cleared). `GraceUntil == nil` is the sole
  "not in grace" signal.
- **chain/`Attest` error** → soft fail-open (D8): keep the session untouched, no
  grace, no notify — we never expire a member because of our own infra outage.

Policy: value is gated on **tier**, not on mere membership. `pending`/dust delegators
get base membership only — a tier needs real active stake (unfakeable), so push
targeting and tier token claims stay Sybil-resistant.

## 5. Token claims (`internal/utils/jose`, S0004 §2.5)

```jsonc
{
  // sub (pseudonym), aud, iss, exp, …
  "pool_membership_state": "active",        // active | pending (none is never issued)
  "active_stake_lovelace": "1234567",       // exact (no bucketing)
  "epochs_active": 17,
  "member_since": "2026-05-01T00:00:00Z",   // start of the current active run
  "tier": "gold"                            // optional first-party opinion; RPs may ignore
}
```

Thin issuer gate: only `pending`/`active` get tokens; `none` → `access_denied`.
Staleness is bounded by a short access-token TTL; refresh re-derives state, so RPs
see transitions by refreshing.

## 6. First-party tier (`PoolConfig.tier_rules`, `membership.TierFor`)

Replaces the deleted rules engine. An ordered JSON array on `PoolConfig`, first
match wins; consumed **only** by the issuer's own channels:

```json
[{"tier":"gold","min_state":"active","min_active_stake":"1000000"},
 {"tier":"silver","min_state":"active","min_active_stake":"100000"},
 {"tier":"basic","min_state":"active"},
 {"tier":"prospect","min_state":"pending"}]
```

There is no rules CRUD engine — edit `PoolConfig.tier_rules` directly. External RPs
never use `tier`; they read the raw facts.

## 7. Delegator enumeration (optional, track C)

`GET /api/admin/delegators?page=N` (viewer) lists the pool's **full on-chain
delegator set** — a superset of `members` (active subscribers). Cold, read-only,
**uncached** Koios `/pool_delegators` passthrough via the optional
`chain.DelegatorLister` capability; sources that can't enumerate → `501`.

## 8. Migrations added by S0004

| # | change |
|---|---|
| 0009 | `StakeSnapshotCache.epochs_active` (reconstruct active snapshot on a cache hit) |
| 0010 | `PoolConfig.tier_rules` (thin first-party tier) |
| 0011 | `DROP TABLE MembershipRule` (rules engine removed) |
