# On-chain identity credentials — Attestor abstraction (S0006)

This document describes the issuer's identity model after the S0006 redesign,
which **generalizes** the single-pool staking attestation of
[`staking-attestation.md`](staking-attestation.md) (S0004) into a pluggable set of
**on-chain identity credentials**. The subject is unchanged (the wallet's stake
credential); what varies is the *kind* of credential attested.

## 1. Model

| Concept | What it is | Code |
|---|---|---|
| **subject** | the wallet's stake credential hash — the identity anchor | — |
| **Attestor** | evaluates one credential against a subject → `{Held, Claim, Facts}` | `core/attestor` |
| **AttestorConfig** | the persisted config for one attestor (`kind`, `label`, `params`, `status`) — the generalization of "the served pool" | `domain.AttestorConfig`, `store.Attestors()` |
| **Set / Result** | evaluates ALL active attestors for a subject, aggregating facts | `attestor.Set.Evaluate` |
| **tier** | first-party opinion from a boolean rule DSL over the aggregate facts | `core/tier` |

`pool_stake` is the only `kind` implemented this cycle (`params = {pool_id,
network, ticker?, name?}`). A `pool_stake` attestor wraps the S0004 derivation
(`membership.DeriveState`): `Held = state != none` (active or pending). NFT and
other kinds are reserved (config-modelled, not yet evaluated).

An issuer configures **N** attestors. The thin gate is **ANY-of**: a subject
holding ≥1 active credential is issued a token. Multiple `pool_stake` attestors =
multi-pool; different `network` values per attestor = cross-chain ready.

## 2. Token shape

The access token carries a self-describing `credentials` array — one entry per
**held** credential — instead of flat single-pool claims:

```jsonc
{
  "iss": "https://pass.example.com",   // OUROPASS_ISSUER; RPs discover JWKS at <iss>/.well-known/ouropass/jwks.json
  "sub": "<pseudonym>", "aud": "...", "exp": 0,
  "credentials": [
    {"kind":"pool_stake","pool":"pool1…","network":"mainnet","state":"active",
     "active_stake_lovelace":"…","epochs_active":17,"member_since":"…"}
  ],
  "tier": "gold"                       // optional first-party opinion; RPs may ignore
}
```

A relying party reads the entries it cares about (`kind=="pool_stake"`, the
`pool` it wants) and applies its own policy. The flat S0004 claims
(`pool_membership_state`, `active_stake_lovelace`, `epochs_active`, `member_since`)
are **gone**. `member_since`/`active_stake_lovelace`/`epochs_active` appear inside
a credential only when that credential is `active`.

## 3. Tier rules (boolean DSL)

`tier_rules` is **issuer-global** (was `PoolConfig.tier_rules`) and evaluates over
the **aggregate** facts. Ordered list, first match wins; no match → no tier (the
subject is not a first-party subscriber).

```jsonc
[
  {"tier":"gold","when":{"fact":"total_active_stake","op":">=","value":"1000000000000"}},
  {"tier":"vip","when":{"all":[
      {"fact":"pool:poolA.state","op":"==","value":"active"},
      {"any":[
        {"fact":"total_active_stake","op":">=","value":"500000000000"},
        {"fact":"nft:policyX.count","op":">=","value":"1"}
      ]}
  ]}},
  {"tier":"basic","when":{"fact":"any_active","op":"==","value":"true"}}
]
```

- Combinators: `all` (AND) / `any` (OR) / `not`; nested. Leaf: `{fact, op, value}`,
  `op ∈ {== != >= > <= <}`. Empty `when` = catch-all. **Not** Turing-complete.
- Named facts the attestor set produces: `any_held`, `any_active`,
  `total_active_stake` (cross-pool sum), and per-attestor `pool:<id>.state` /
  `pool:<id>.active_stake_lovelace` / `pool:<id>.epochs_active`.

Edit via the **Tiers** admin page or `POST /api/admin/pool/tier-rules`.

## 4. Configuration & migration

| Env var | Change |
|---|---|
| `OUROPASS_POOL_ID` | **Removed.** The served pool(s) are now `pool_stake` attestors configured in the admin UI / `POST /api/admin/attestors`. |
| `OUROPASS_ISSUER` | **Required.** Token `iss` and issuer deployment identity — a public base URL (e.g. `https://pass.example.com`). Also the first-party subscription/admin scope. |
| `OUROPASS_NETWORK` | Now only the **default network for new attestors**; the authoritative network is per-attestor (`params.network`). |

The active-membership cache (`core/membership.CachedSource`) is **pool-agnostic**
(stores the credential's real active pool; every `pool_stake` attestor on a network
shares it) and **network-scoped** (`StakeSnapshotCache` keyed by
`(stake_credential_hash, network)`).

**Migration of an existing deployment** (migration `0012`): each `PoolConfig` row
is projected into one `pool_stake` `AttestorConfig`, and its `tier_rules` lift to
the issuer-global `IssuerConfig`. After upgrading, set `OUROPASS_ISSUER` and drop
`OUROPASS_POOL_ID`. **Cold start**: with zero attestors configured, no subject
passes the gate; an owner (authenticated by `OUROPASS_OWNER_KEYS`) logs in and adds
attestors, which take effect on the next issuance (resolved per-call).

## 5. Admin API

| Method / path | Role | Purpose |
|---|---|---|
| `GET /api/admin/attestors` | viewer | list configured attestors |
| `POST /api/admin/attestors` | operator | create `{kind, label, params}` |
| `POST /api/admin/attestors/{id}` | operator | update `label`/`params`/`status` |
| `DELETE /api/admin/attestors/{id}` | operator | remove |
| `GET /api/admin/pool` · `POST /api/admin/pool/tier-rules` | viewer · operator | read / set issuer-global tier_rules |

All mutations are audit-logged. UI: **Attestors** and **Tiers** pages.
