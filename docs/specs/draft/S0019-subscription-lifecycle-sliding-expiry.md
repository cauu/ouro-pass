# Subscription lifecycle: membership-driven expiry + grace + notifications + tier refresh

Spec-ID: S0019
Status: draft
Created Time: 2026-07-01T11:52:34+08:00
Start Time:
Completion Time:
Previous Spec-ID: S0018
Closure Reason:

## 1. Requirement Details

### Background

A subscription's `ExpiresAt` is set once at creation to a hardcoded `sessionTTL =
30 days` (`telegram.go:24`), but **nothing ever enforces it** — no code compares
`now > ExpiresAt` for subscriptions, and `ListActive*` filters on `status='active'`
only. The real expiry is membership-driven: the per-epoch reconciler (`reconciliation.go`)
sets `status=expired` when the credential's on-chain state becomes `none`. So today:

- The displayed "Expires: <date>" (`/status`, telegram.go:150) is misleading — the
  session actually persists as long as membership holds; the 30-day TTL is dead.
- Expiry is silent — the reconciler only flips status; the user is never told, and only
  finds out by running `/status` or noticing pushes stopped.
- The reconciler refreshes `LastVerifiedAt` but **not `tier`** (it calls `Membership`
  = state-only, not `Attest` = state+tier). So a `pending` member who later becomes
  `active` keeps an empty/stale tier until they re-bind (the second half of the empty-tier
  issue; the first half — the Koios `pool_id_bech32` parse — was fixed in S0018 p2-1).

We validated the membership timing model (entry/leaving each lag ~2 epochs; the leaving
tail keeps a departed delegator `active` for ~2 epochs — an inherent grace; a returning
delegator passes through a `pending` window). `pending` is treated as a member — good UX,
but it grants **base membership** to dust/Sybil delegators (it can never grant a tier:
tier needs real active stake, which is unfakeable). Policy (option 1): gate all value on
**tier**, not on mere membership — which the platform already supports (push `TargetTier`,
tier claim in tokens) and which relies on tier being accurate.

### Scope

- **Sliding informational validity (approach B):** each successful per-epoch re-verify of
  a member (`active`/`pending`) refreshes `LastVerifiedAt`; the displayed `ExpiresAt`
  = `LastVerifiedAt + TTL` (a friendly "valid through, auto-renews" date). For a member
  this is **informational only** — it is never an enforcement deadline.
- **Expiry is membership-loss only.** Expiry is driven exclusively by a definitive
  `state == none` (the user genuinely left). There is **no time-based / operational
  expiry**: a chain-query error is always fail-open (keep the session, no notify, no
  grace) — we never expire a user because of our own infra outage ("don't disturb").
- **Grace + terminal expiry on membership loss (deadline timestamp, not a reconcile
  count):** the first reconcile that sees `state == none` for an active subscription does
  **not** expire it — it records a grace **deadline** (`now + GRACE`, a wall-clock
  timestamp) and notifies the user once. Expiry is then `state == none && now >= deadline`
  — a pure time comparison, independent of how often the reconciler runs (frequency only
  affects detection latency, never the criterion). If the member recovers
  (`pending`/`active`) before the deadline, the session is restored (no re-bind).
- **Tier refresh:** switch the reconciler from `Membership` (state) to `Attest`
  (state+tier) and write the fresh `tier` back each epoch, so `pending→active` upgrades
  and stake-driven tier changes are reflected automatically.
- **Expiry notification:** a one-shot bot DM to the affected user when a subscription
  enters grace, reusing the push transport routing.
- **`/status` truthfulness:** show the real `ExpiresAt` (= LastVerifiedAt + TTL) and
  `LastVerifiedAt`; for a grace session, indicate it is expiring.
- **Policy + foolproofing (option 1, p3-1):** value must be gated on tier; make the admin
  push modal enforce that (a required tier select instead of a free-text box whose blank
  default silently broadcasts to everyone).
- **Koios free-tier key config (option A):** the existing `OUROPASS_CHAIN_API_KEY` (already
  sent as `Authorization: Bearer`) is the one lever that avoids rate limits — a free
  registered koios.rest key lifts the quota from the public tier (5,000/day, 10 RPS) to the
  registered free tier (50,000/day). Make it discoverable and correctly described: fix the
  misleading ".env.example"/deployment copy (it currently says "paid tiers" only) and add an
  optional installer prompt. Env stays the home (a single boot-time infra credential, per
  the config-secrets convention + [[installer-scope-boundary]]).

### Constraints

- No change to `DeriveState` / eligibility semantics or the Koios adapter — subscription
  lifecycle + tier bookkeeping layer only.
- Reconciler is **fail-open** (D8): only a definitive `state == none` triggers grace /
  expiry / notify; a chain/`Attest` error keeps the session untouched and silent.
- Notifications are best-effort: a send failure is logged, never blocks reconcile, and the
  warning is sent **once** on grace-entry (not repeated while still `none`).
- Grace is a **wall-clock duration `GRACE` ≈ 1 epoch** (e.g. `5d` ≈ 1 mainnet epoch),
  stored as a deadline timestamp when membership is lost. It must comfortably exceed one
  reconcile cadence so a re-delegated member's `pending` is observed before the deadline;
  the leaving tail already provides ~2 epochs before grace even starts. Reconcile
  re-derives membership **before** the expiry check, so a member recovered near the
  deadline is restored, not expired.

### Non-goals

- Time-based / operational expiry and any notification for it (dropped by design above).
- Admin-configurable TTL/grace (consts this spec; admin config is future).
- Non-telegram channel notifications (telegram only now; the notifier is channel-typed to
  extend later).
- Moving the Koios key into the DB / an admin "Chain settings" page (option B) — that is a
  future consolidation alongside the deferred self-hosted-endpoint admin setting (S0015);
  this spec keeps the key in env (option A).
- Reconcile request batching / pacing / local-epoch trigger — with the registered free-tier
  key (50k/day) the reconcile load is comfortable for typical pools; a hard throttle /
  batching is a future optimization for very large pools, not this spec.

## 2. Outline Design

- **`reconciliation.StateEvaluator`**: change the method from
  `Membership(ctx, sch) (State, error)` to `Attest(ctx, sch) (State, string, error)`
  (the `oauth.Server` already implements both). `Reconcile` uses the returned `tier`.
- **Two separate fields, two separate meanings** (do NOT overload one field): keep
  `expires_at` as the purely **informational** "valid through / auto-renews" date
  (= `LastVerifiedAt + TTL`), never used for enforcement; add a **nullable `grace_until`**
  (`*time.Time`) that is the real expiry **deadline**, set ONLY while in grace. `grace_until
  == NULL` unambiguously means "not in grace" — no far/near inference, robust to reconcile
  outages (a stale informational `expires_at` is never mistaken for a grace deadline).
- **`Reconcile` per active session** (`reconciliation.go`) — re-derive membership FIRST:
  - `Attest` errors → fail-open (keep, count Failed, no grace, no notify) — unchanged.
  - member (`active`/`pending`): `LastVerifiedAt = now`; `Tier = tier`;
    `ExpiresAt = now + TTL`; `GraceUntil = nil` (clears grace / restores).
  - `state == none`:
    - `GraceUntil == nil` (first loss) → `GraceUntil = now + GRACE`; **notify once**.
    - `GraceUntil != nil`: `now >= *GraceUntil` → `status = expired`; else keep.
  - Expiry criterion is the timestamp `now >= *GraceUntil`, not a reconcile count — reconcile
    frequency changes only how quickly expiry is *noticed*, never *when it is due*.
- **Notifier seam**: `reconciliation.New(...)` gains a `Notifier`, e.g.
  `func(ctx, sess domain.SubscriptionSession, msg string) error`. `main.go` wires it to
  resolve the session's channel instance token (`instanceToken`) + `telegram.NewBotAPITransport`
  and `SendMessage(channel_user_id, msg)` — the same routing the push worker uses. nil
  Notifier → notifications skipped (tests / degraded).
- **`/status`** (`telegram.go`): show `ExpiresAt` (= LastVerifiedAt + TTL) + `LastVerifiedAt`;
  reflect grace ("expiring — re-delegate to keep your membership").
- **Push modal (p3-1)** (`web/src/features/push/PushPage.tsx`): replace the free-text
  `Target tier` `Input` with a **required `<select>`** populated from configured tiers
  (`getPool()` → `tier_rules[].tier`, deduped — same source as `TierRulesSection`), plus an
  explicit **"All members (no tier gate)"** option so an untargeted broadcast is a conscious
  choice, not the silent blank default. Optional backend guard: drop untargeted broadcasts to
  tier-less subscriptions. (Aside, out of scope: Channel / topic / entitlement are also
  free-text and could later become selects; not a freeload gate.)
- **Koios key config (p1-6, option A)**: no core code change (`OUROPASS_CHAIN_API_KEY` is
  already read into `cfg.ChainAPIKey` and sent as `Authorization: Bearer` — `koios.go:218`).
  Fix `.env.example` + `docs/deployment.md` copy to explain the tiers (public 5k/day·10 RPS
  → registered free 50k/day via a free koios.rest key; blank = public). Add an OPTIONAL
  installer prompt in `deploy/install.sh` (`ask CHAIN_API_KEY … ""`, blank default so
  `--non-interactive` is unaffected) that `set_env OUROPASS_CHAIN_API_KEY`.
- **Docs**: record the lifecycle (membership-driven expiry, 1-epoch grace, notify, tier
  refresh) and the option-1 policy (value on tier; `pending`/dust = base membership only).

### Risk and rollback

- Risk: introducing grace bookkeeping / a new field. Mitigation: additive migration; a
  member reconcile clears grace, so existing active members are unaffected. Rollback = git
  revert.
- Risk: notification plumbing errors. Mitigation: best-effort, fail-open, logged, one-shot.

## References

- `internal/worker/reconciliation/reconciliation.go`, `internal/core/oauth/oauth.go`
  (`Membership`/`Attest`/`attest`/`firstPartyTier`), `internal/worker/telegram/telegram.go`
  (`sessionTTL`, `/status`, activate), `internal/worker/push/push.go` + `cmd/issuer/main.go`
  (`instanceToken`, transport routing), `internal/store/repo_subscriptionsession.go`,
  `web/src/features/push/PushPage.tsx` + `web/src/api/admin.ts` (`getPool`).
  Prior: S0018 p2-1 (Koios `pool_id_bech32` — the other half of accurate tier).

## 3. Execution Plan

- [ ] p1-1 Reconciler refreshes tier: switch `StateEvaluator` to `Attest` (state+tier);
      write `Tier` back on each member re-verify. Update reconcile tests (mock returns tier;
      assert `pending→active` upgrades the stored tier).
- [ ] p1-2 Membership-driven grace + expiry (deadline timestamp): add a nullable
      `grace_until` column (additive migration; keep `expires_at` informational). Re-derive
      membership first; member → `LastVerifiedAt=now`, `ExpiresAt=now+TTL`, `GraceUntil=nil`;
      first `none` (`GraceUntil==nil`) → `GraceUntil=now+GRACE`; in-grace `none` with
      `now >= *GraceUntil` → `status=expired`; recovery before the deadline → restore
      (`GraceUntil=nil`). Consts `subscriptionTTL` + `subscriptionGrace`. Tests: slide /
      grace-entry / restore-before-deadline / expire-after-deadline / fail-open-on-error /
      outage-then-none-still-gets-grace.
- [ ] p1-3 Expiry notification: `Notifier` seam on the reconciler; `main.go` wires the
      telegram transport (per-instance token routing); send the grace warning once on
      grace-entry; best-effort + fail-open. Test the seam is called once with the right
      session/message and that send errors don't fail reconcile, and no DM on the 2nd `none`.
- [ ] p1-4 `/status` + docs: show real `ExpiresAt` + `LastVerifiedAt` + grace wording;
      document the lifecycle and the option-1 policy.
- [ ] p1-5 Push-modal foolproofing (p3-1): required tier `<select>` from configured tiers +
      explicit "All members (no tier gate)" option in `PushPage`; (optional) backend guard
      dropping untargeted broadcasts to tier-less subscriptions. Web typecheck + test.
- [ ] p1-6 Koios free-tier key config (option A): fix `.env.example` + `docs/deployment.md`
      copy (tiers: public 5k/day·10 RPS → registered free 50k/day via a free koios.rest key;
      blank = public); add an optional blank-default installer prompt for
      `OUROPASS_CHAIN_API_KEY`. No core code change (already Bearer-plumbed). shellcheck clean.
- [ ] p2-1 Validation: `make test` + `pnpm test` + `shellcheck deploy/install.sh`.

## 4. Test and Acceptance Criteria

- TC-1 Tier refresh: a subscription created while `pending` (empty tier) has its stored
  `tier` set correctly once the credential becomes `active` at the next reconcile — no
  re-bind. `Membership`→`Attest` swap verified.
- TC-2 Member never expires on time: repeated reconciles of a still-member session keep it
  active and slide `ExpiresAt` (= LastVerifiedAt + TTL); a chain/`Attest` error keeps it and
  does NOT enter grace or notify (fail-open).
- TC-3 Grace on loss (deadline-driven): first `state == none` sets `GraceUntil = now + GRACE`
  (leaving `expires_at` informational) and does not expire; recovery to `pending`/`active`
  before the deadline restores it (`GraceUntil` cleared, no re-bind); `state == none` with
  `now >= *GraceUntil` → `status = expired`. Expiry is decided by the `GraceUntil` timestamp,
  not by how many reconcile passes occurred; `GraceUntil == nil` is the sole "not in grace"
  signal (no far/near inference), including after a reconcile outage.
- TC-4 Notification: entering grace triggers exactly one bot DM to the session's user via
  the correct instance transport; a send error is logged and does not fail the pass; no DM on
  the second consecutive `none`.
- TC-5 `/status`: shows the real `ExpiresAt` and `LastVerifiedAt`, and grace wording when
  expiring.
- TC-6 Push modal: `Target tier` is a required select from the configured tiers with an
  explicit "All members" option; a broadcast-to-all is only sent when that option is chosen.
- TC-7 Regression: `make test` + `pnpm test` green; `shellcheck deploy/install.sh` clean.
- TC-8 Koios key config: `.env.example` + `docs/deployment.md` describe the registered free
  tier (50k/day via a free koios.rest key) vs the public tier (5k/day, 10 RPS), not "paid
  tiers" only; the installer optionally prompts and writes `OUROPASS_CHAIN_API_KEY`; blank
  still works (public tier). shellcheck clean; a set key is sent as `Authorization: Bearer`.

Pass/fail: TC-1..TC-8 pass; no change to `DeriveState`/eligibility/Koios semantics.

## 5. Execution Log (append-only)

- 2026-07-01T11:52:34+08:00 spec drafted (S0019) after the subscription-lifecycle design
  discussion. Approach B (informational sliding `ExpiresAt`) + membership-driven grace/expiry
  + one-shot expiry DM + reconciler tier refresh (`Membership`→`Attest`); option-1 freeload
  policy + push-modal foolproofing.
- 2026-07-01T12:10:00+08:00 revised per review: (1) dropped time-based/operational expiry
  entirely — expiry is membership-loss only, chain errors always fail-open, no infra-outage
  expiry ("don't disturb"); (2) grace fixed at ≈1 epoch via consecutive-`none` detection
  (network-adaptive, membership re-derived before the expiry check) — not "≤1 epoch"; the
  leaving tail already provides ~2 epochs; (3) promoted the push-modal foolproofing to a
  first-class item (p1-5): the current `Target tier` is a free-text box whose blank default
  silently broadcasts to everyone — replace with a required tier select + explicit
  "All members" option. Remaining decision: TTL value for the informational `ExpiresAt`
  (default 30d) and the grace-marker choice (`grace_since` vs `expiring` status) at p1-2.
- 2026-07-01T12:30:00+08:00 revised the expiry criterion per review: the "2nd consecutive
  `none`" rule tied expiry to reconcile frequency (unintuitive, fragile). Replaced with a
  **deadline timestamp**: on membership loss set `ExpiresAt = now + GRACE`; expire when
  `state == none && now >= ExpiresAt`. Frequency-independent (only detection latency depends
  on cadence). `GRACE` = wall-clock const ≈ 1 epoch (`< TTL`), stored in the existing
  `ExpiresAt` column (no new field; the far-vs-near `ExpiresAt` distinguishes member vs
  in-grace and makes notify-once automatic). Supersedes the `grace_since`/`expiring`
  question. Remaining decision: TTL (default 30d) + GRACE (default ~5d ≈ 1 mainnet epoch).
- 2026-07-01T12:45:00+08:00 reverted the single-field reuse per review: overloading
  `expires_at` for both the informational "auto-renews" date and the hard grace deadline is
  fragile — a reconcile outage can age a member's `expires_at` into the "near" band and get it
  misjudged as an expired grace (instant expiry, no notice). Split into TWO fields: keep
  `expires_at` purely informational; add a nullable `grace_until` set only on loss. Enforcement
  = `grace_until != nil && now >= *grace_until && state==none`; `grace_until == nil` is the sole
  "not in grace" signal (unambiguous, outage-safe — a post-outage `none` still gets a proper
  grace + notify). Costs one additive nullable column.
- 2026-07-01T13:05:00+08:00 added p1-6 (Koios free-tier key config, option A) after the
  rate-limit research: public tier is 5,000/day @ 10 RPS, the registered free tier is
  50,000/day via a free koios.rest key, which comfortably covers reconcile for typical pools.
  The key already exists (`OUROPASS_CHAIN_API_KEY`, Bearer-plumbed) but the copy misleadingly
  says "paid tiers" and there's no installer prompt → p1-6 fixes the copy + adds an optional
  blank-default prompt; env stays the home. Option B (admin "Chain settings" page, DB-stored
  key + the deferred self-hosted endpoint) and reconcile batching/pacing/local-epoch trigger
  are explicit non-goals (future). Remaining decision unchanged: TTL (30d) + GRACE (~5d).

## 7. Change Requests (append-only)
