# Subscription lifecycle: membership-driven expiry + grace + notifications + tier refresh

Spec-ID: S0019
Status: completed
Created Time: 2026-07-01T11:52:34+08:00
Start Time: 2026-07-01T14:32:06+08:00
Completion Time: 2026-07-01T17:07:53+08:00
Previous Spec-ID: S0018
Closure Reason: delivered

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
- `TTL = 30 days` (informational `ExpiresAt`) and `GRACE = 5 days` (≈ 1 mainnet epoch;
  `GRACE < TTL`). Grace is a wall-clock duration stored as a deadline timestamp when
  membership is lost; 5d comfortably exceeds one reconcile cadence so a re-delegated
  member's `pending` is observed before the deadline (and the leaving tail already provides
  ~2 epochs before grace even starts). Reconcile re-derives membership **before** the expiry
  check, so a member recovered near the deadline is restored, not expired.

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

- [x] p1-1 Reconciler refreshes tier: switch `StateEvaluator` to `Attest` (state+tier);
      write `Tier` back on each member re-verify. Update reconcile tests (mock returns tier;
      assert `pending→active` upgrades the stored tier).
- [x] p1-2 Membership-driven grace + expiry (deadline timestamp): add a nullable
      `grace_until` column (additive migration; keep `expires_at` informational). Re-derive
      membership first; member → `LastVerifiedAt=now`, `ExpiresAt=now+TTL`, `GraceUntil=nil`;
      first `none` (`GraceUntil==nil`) → `GraceUntil=now+GRACE`; in-grace `none` with
      `now >= *GraceUntil` → `status=expired`; recovery before the deadline → restore
      (`GraceUntil=nil`). Consts `subscriptionTTL = 30 * 24h` + `subscriptionGrace = 5 * 24h`.
      Tests: slide /
      grace-entry / restore-before-deadline / expire-after-deadline / fail-open-on-error /
      outage-then-none-still-gets-grace.
- [x] p1-3 Expiry notification: `Notifier` seam on the reconciler; `main.go` wires the
      telegram transport (per-instance token routing); send the grace warning once on
      grace-entry; best-effort + fail-open. Test the seam is called once with the right
      session/message and that send errors don't fail reconcile, and no DM on the 2nd `none`.
- [x] p1-4 `/status` + docs: show real `ExpiresAt` + `LastVerifiedAt` + grace wording;
      document the lifecycle and the option-1 policy.
- [x] p1-5 Push-modal foolproofing (p3-1): required tier `<select>` from configured tiers +
      explicit "All members (no tier gate)" option in `PushPage`; (optional) backend guard
      dropping untargeted broadcasts to tier-less subscriptions. Web typecheck + test.
- [x] p1-6 Koios free-tier key config (option A): fix `.env.example` + `docs/deployment.md`
      copy (tiers: public 5k/day·10 RPS → registered free 50k/day via a free koios.rest key;
      blank = public); add an optional blank-default installer prompt for
      `OUROPASS_CHAIN_API_KEY`. No core code change (already Bearer-plumbed). shellcheck clean.
- [x] p2-1 Validation: `make test` + `pnpm test` + `shellcheck deploy/install.sh`.

### Post-review fixes (multi-agent review, appended before closure — S0019 review summary)

- [x] p3-1 Tier-wipe fail-open fix (review P2-1): `firstPartyTier` now returns
      `(string, error)` and `attest` propagates a tier_rules **read error** instead of
      swallowing it to `""`. Issue/activation fail-closed; the reconciler fail-opens and
      keeps the stored tier (D8). A genuine no-match still yields `("", nil)` so legit tier
      downgrades still write `""`. Test: reconcile-with-error preserves the stored tier. (TC-9)
- [x] p3-2 `grace_until` NULL round-trip store test (review P2-2): direct repo test that
      Upsert→Get preserves NULL vs a set deadline and that clearing writes NULL. (TC-10)
- [x] p3-3 Push-modal gate automated test (review P2-3, closes TC-6 gap): RTL test that an
      unselected tier blocks submit and that choosing "All members" omits `target.tier`. (TC-11)
- [x] p3-4 `outage-then-none-still-gets-grace` reconcile test (review P2-5): pass1 `Attest`
      error (kept, no grace) → pass2 `none` still opens grace + notifies once. (TC-12)
- [x] p3-5 Consolidate the duplicate 30d TTL const (review P3-6): a single shared const so
      the activation-display TTL and the reconcile-slide TTL cannot drift. (TC-13)
- [x] p3-6 Verification harness (acceptance aid): extend `cmd/devflow` with a narrated
      "3. Subscription lifecycle" section that runs the REAL reconciler against the mock chain
      and calls `Reconcile()` directly, so the epoch/time-driven behavior (tier refresh, grace
      + one-shot DM, notify-once, restore, terminal expiry) is watchable in seconds — with the
      live `/status` text at grace. Solves "hard to accept" without waiting days. (TC-14)
- [x] p3-7 Push-modal Channel select (user follow-up during acceptance): replace the free-text
      `channel_type` Input with a required `<Select>` of the pool's ACTIVE channel instances
      (`listChannels()`, same source as the Channels page). The push now targets a specific
      configured bot via `channel_id` (channel_type derived from the chosen instance) instead of
      a hand-typed type string. Disabled instances are excluded. Web typecheck + RTL tests. (TC-15)
- [x] p3-9 Harden reconcile tests — pin "expiry is deadline-driven, not pass-count" (test-review
      P1): a multi-agent audit of the lifecycle TESTS found the headline invariant was not actually
      pinned — a count-based-expiry regression passed the whole suite (the existing expiry test
      forced `GraceUntil` into the past, so timestamp and count impls both expired). Add
      `TestReconcile_ExpiryIsDeadlineDrivenNotPassCount` driving an INJECTED clock: stays in grace
      across multiple `none` passes while the deadline is future (asserts not-expired, deadline
      unchanged), pins `GraceUntil == now+GRACE` and the `now >= *GraceUntil` (==) boundary via
      natural elapse; and strengthen `NotifiesOnceOnGrace` pass-2 to assert `Expired==0` + active.
      Verified with a mutation (count-based expiry) — the new tests FAIL, the old ones didn't. (TC-17)
- [x] p3-8 Drop the dead Target topic / Required entitlement inputs (user follow-up): nothing
      populates a subscription's `Topics`/`Entitlements` (activation sets them nil; no admin/bot/
      API/UI path; §7.1 unbuilt), so `matches()` filtering on them always yields ZERO recipients —
      a silent footgun like the p1-5 tier default. Remove both inputs from the New-push form (target
      now carries only tier); the detail drawer still displays them for historical jobs; backend
      filter untouched. Re-add when topic-subscription / entitlement-grant lands. (TC-16)

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

- TC-9 Tier not wiped on tier_rules read error: a member reconcile whose `Attest` fails
  (incl. a tier_rules read failure) keeps the session active, out of grace, with its stored
  `tier` preserved (not cleared to "").
- TC-10 `grace_until` NULL round-trip: repo Upsert→Get preserves NULL vs a set deadline; a
  clearing Upsert writes NULL back.
- TC-11 Push modal gate (automated): submitting with no tier selected fails validation; the
  "All members" choice sends no `target.tier`.
- TC-12 Outage-then-none still gets grace: a pass where `Attest` errors (kept, no grace)
  followed by a `none` pass still opens grace + notifies once.
- TC-13 Single TTL const: activation display and reconcile slide share one constant.
- TC-14 Verification harness: `go run ./cmd/devflow` narrates section 3 driving the real
  reconciler through tier-refresh → grace+DM → notify-once → restore → expiry in seconds,
  with the live `/status` text, so the lifecycle is acceptance-verifiable without waiting.
- TC-15 Push Channel select: the push modal Channel is a required select of active channel
  instances; submit is blocked with no channel; a disabled instance is not offered; the created
  job carries the chosen `channel_id` + derived `channel_type`.
- TC-16 No dead topic/entitlement inputs: the New-push form no longer renders Target topic /
  Required entitlement inputs (they only ever narrowed delivery to zero); the created job's
  target carries at most a tier.
- TC-17 Expiry is deadline-driven, not pass-count: with an injected clock, a session in grace
  survives multiple `none` reconciles while `now < GraceUntil` (not expired, deadline unchanged)
  and expires exactly when `now >= GraceUntil`; a count-based-expiry mutation fails the suite.

Pass/fail: TC-1..TC-17 pass; no change to `DeriveState`/eligibility/Koios semantics.

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
- 2026-07-01T13:20:00+08:00 values locked per user: `subscriptionTTL = 30d`,
  `subscriptionGrace = 5d` (≈ 1 mainnet epoch, `< TTL`). No open decisions remain; ready to
  promote to active.
- 2026-07-01T14:32:06+08:00 promoted draft → active (Previous Spec-ID S0018). Beginning p1-1.
- 2026-07-01T14:50:00+08:00 all plan items p1-1..p1-6 + p2-1 delivered and committed; TC-1..TC-8
  evidence appended. Full validation (make test + pnpm test/typecheck + shellcheck) green. Spec
  held OPEN pending user verification (per user: "等我验收后再close").

## 6. Validation Evidence (append-only)

- TC-1 | stack: go | command: go test ./internal/worker/reconciliation/ ./internal/e2e/ | result: pass | note: StateEvaluator swapped Membership→Attest; TestReconcile_RefreshesTier asserts a pending(empty-tier)→active session gets tier=gold at reconcile with no re-bind; e2e reconcile still expires on membership loss.
- TC-2/TC-3 | stack: go | command: go test ./... | result: pass | note: added nullable grace_until column (migration 0016 sqlite+postgres) + domain field + repo Upsert/scan. Reconcile re-derives membership first: member → slide ExpiresAt=now+30d, clear GraceUntil; first none → GraceUntil=now+5d (status stays active); none with now>=deadline → expired; recovery before deadline restores. TestReconcile_KeepAndGrace (slide + grace-entry, no immediate expiry), TestReconcile_GraceLifecycle (grace→restore→re-grace→expire-after-deadline), TestReconcile_FaultIsolation (chain error keeps session, no grace), e2e two-pass grace→expire. Full server suite green.

- TC-4 | stack: go | command: go test ./internal/worker/reconciliation/ ./cmd/issuer/ | result: pass | note: Notifier seam (WithNotifier) on the reconciler; grace-entry fires it once with the session + graceMessage. TestReconcile_NotifiesOnceOnGrace asserts one call on entry, that a notifier error doesn't fail the pass, and no second DM while still none. main.go wires a telegram-only notifier reusing the push per-instance token routing (st.Channels().Get → instanceToken → NewBotAPITransport.SendMessage to channel_user_id).

- TC-5 | stack: go | command: go test ./internal/worker/telegram/ | result: pass | note: /status now shows Tier, Status, "Last verified", "Valid through (auto-renews …)" (= LastVerifiedAt+TTL), and an ⚠️ "Expiring on … Re-delegate…" line when GraceUntil is set. TestStatusAndUnsubscribe asserts the member format + the grace warning. sessionTTL comment corrected (informational, mirrors reconciliation.subscriptionTTL). Lifecycle + option-1 policy documented in docs/staking-attestation.md §4.

- TC-6 | stack: ui | command: pnpm typecheck && pnpm test | result: pass | note: PushPage `Target tier` is now a required <Select> populated from getPool().tier_rules[].tier (deduped, same source as the tier-rules editor) with a disabled "Select…" placeholder + explicit "All members (no tier gate)" sentinel (tierAllMembers="__all__", mapped back to no tier gate in the request). A blank no longer silently broadcasts to everyone; broadcast-to-all requires choosing that option. Optional backend guard intentionally omitted (would change delivery semantics; foolproofing goal met at the UI). typecheck clean; full web suite green.

- TC-8 | stack: shell | command: shellcheck deploy/install.sh | result: pass | note: .env.example + docs/deployment.md copy rewritten — public tier (~5k/day, 10 RPS) vs free registered koios.rest tier (~50k/day, Bearer), not "paid tiers" only, with the 429 guidance + signup link. install.sh gains an optional `ask CHAIN_API_KEY … "${OURO_CHAIN_API_KEY:-}"` (blank default → --non-interactive unaffected) + `set_env OUROPASS_CHAIN_API_KEY`. No core code change (already Bearer-plumbed at koios.go:218). shellcheck clean.

- TC-7 | stack: go | command: make -C server test | result: pass | note: full server suite green (count=1), incl. reconciliation, telegram, e2e, store (migration 0016 applies).
- TC-7 | stack: ui | command: pnpm test && pnpm typecheck | result: pass | note: web suite (10 tests) green; tsc -b clean.
- TC-7 | stack: shell | command: shellcheck deploy/install.sh | result: pass | note: clean. Also `go vet ./...` clean; gofmt clean on all S0019-touched files (2 pre-existing unformatted files outside this spec left untouched per append-only/scope discipline).

- TC-9 | stack: go | command: go test ./internal/core/oauth/ ./internal/worker/reconciliation/ ./internal/e2e/ | result: pass | note: firstPartyTier→(string,error); attest propagates tier_rules read error (oauth.go). Reconciler already fail-opens on Attest error; TestReconcile_FaultIsolation now also asserts the errored member's stored tier stays "gold" (not wiped). Happy-path oauth/token/activation suites unchanged (GetTierRules succeeds → no error).

- TC-10 | stack: go | command: go test ./internal/store/ -run GraceUntilRoundTrip | result: pass | note: TestSubscriptionRepo_GraceUntilRoundTrip — nil insert → NULL/nil; set deadline → preserved (ms-equal); clearing Upsert → NULL again.

- TC-11 | stack: ui | command: pnpm test src/features/push/PushPage.test.tsx | result: pass | note: RTL — (1) submit with no tier selected is blocked ("Pick a tier…" shown, createPushJob not called); (2) "All members" (__all__) → target={} (no tier); (3) "gold" → target={tier:"gold"}. This closes the TC-6 automated-coverage gap (TC-6 now fully, not partially, met). Full web suite 13 tests green; typecheck clean.

- TC-12 | stack: go | command: go test ./internal/worker/reconciliation/ -run OutageThenNone | result: pass | note: TestReconcile_OutageThenNoneGetsGrace — pass1 Attest error → Failed1/Grace0, session kept, tier preserved, no notify; pass2 none → Grace1, GraceUntil set, notified exactly once. Proves grace keys on GraceUntil==nil, not pass count.

- TC-13 | stack: go | command: go test ./internal/worker/reconciliation/ ./internal/worker/telegram/ ./internal/domain/ | result: pass | note: single source of truth `domain.SubscriptionTTL`/`SubscriptionGrace`; reconciliation `subscriptionTTL/Grace` and telegram `sessionTTL` now bind to it (compile-time). TestLifecycleConstsShared + TestSessionTTLSharedWithDomain assert equality so drift is caught.

- TC-7(re-run) | stack: go+ui+shell | command: make -C server test && pnpm test && pnpm typecheck && shellcheck deploy/install.sh | result: pass | note: full regression after p3-1..p3-5 — server suite all green, web 13 tests green, tsc clean, shellcheck clean.

- TC-14 | stack: go | command: go run ./cmd/devflow | result: pass | note: section "3. Subscription lifecycle" runs the real reconciler + real /status against the mock chain; observed live: 3a pending(tier="")→active(tier=gold); 3b first none → grace=1, 1 DM, /status shows "⚠️ Expiring on …"; 3c second none → grace=0, DM count unchanged (1); 3d re-delegate → grace cleared, gold restored; 3e none→grace then grace_until forced past → expired=1, status=expired. Every transition visible in seconds.

- TC-15 | stack: ui | command: pnpm test src/features/push/PushPage.test.tsx && pnpm typecheck | result: pass | note: Channel is now a required <Select> of active instances from listChannels(); PushForm carries channel_id, channel_type derived from the chosen instance. RTL: blocked with no channel ("Pick a channel"), disabled "Old" instance excluded from options, created job has channel_id="tg1"+channel_type="telegram". Full web suite 15 tests green; tsc clean. Embed rebuilt (bundle contains "Pick a channel").

- TC-16 | stack: ui | command: pnpm test src/features/push/PushPage.test.tsx && pnpm typecheck | result: pass | note: removed the Target topic + Required entitlement inputs from the New-push form (PushForm drops topic/entitlement; target builds only tier). RTL asserts input[name=topic]/[name=entitlement] are absent. Rationale: no code path populates a subscription's Topics/Entitlements, so filtering on them always matched zero recipients (silent footgun). Backend matches() untouched; detail drawer still shows historical values. Web suite green; tsc clean; embed rebuilt.

- TC-17 | stack: go | command: go test -count=1 ./internal/worker/reconciliation/ | result: pass | note: added TestReconcile_ExpiryIsDeadlineDrivenNotPassCount (injected clock: in-grace passes with future deadline → Expired0/active/deadline unchanged; at deadline == → expired) + NotifiesOnce pass2 now asserts Expired0/active. Mutation-verified: patching the impl to expire on the 2nd `none` (ignoring the deadline) FAILS both new checks while GraceLifecycle/KeepAndGrace stayed green (exactly the false-confidence gap the audit flagged). go vet clean.

## 7. Change Requests (append-only)

- 2026-07-01T17:07:53+08:00 CLOSED (delivered). Second full multi-agent review (round 2, covering
  p3-1..p3-9; `code_review/S0019-round2/`) returned APPROVE from claude + cursor, no P0/P1 — all
  findings were accepted deferrals or P3 notes (sync notification, p3-1 availability tradeoff,
  activation asymmetry, legacy empty-ChannelID DM, push single-instance narrowing; one cursor
  false-positive on the client channel_type fallback cleared — server-side 400 guard). Plan items
  p1-1..p1-6, p2-1, p3-1..p3-9 all [x]; TC-1..TC-17 evidenced; full regression green. User closed.
- 2026-07-01T16:54:00+08:00 multi-agent audit of the reconcile lifecycle TESTS (claude+cursor,
  same P1; codex rate-limited): the "expiry is deadline-driven, not reconcile-pass-count" invariant
  was not actually pinned — a count-based regression passed the whole suite. Added p3-9 (TC-17):
  injected-clock discriminator test + strengthened NotifiesOnce, mutation-verified to have teeth.
  Remaining audit P2/P3 (tier-change test, re-notify on re-entry, ListActive exclusion, past-
  expires_at active, tautological const test, stale comments) noted in code_review summary, not
  taken this round per user scope ("补 P1 + 注入时钟").
- 2026-07-01T16:39:00+08:00 user asked what Target topic / Required entitlement do → found they
  filter push against a subscription's Topics/Entitlements, which NOTHING ever populates (§7.1
  unbuilt), so any value silently narrows delivery to zero. User chose to remove the inputs.
  Appended p3-8 (TC-16). Backend filter + schema kept for when the feature lands.
- 2026-07-01T16:28:00+08:00 user follow-up during acceptance: make the push-modal Channel a
  dropdown too (in-place, no new spec). Appended p3-7 (TC-15): Channel → required select of
  active channel instances (channel_id), channel_type derived. Also documented that the admin
  SPA is compile-time embedded, so a frontend change needs `make -C server web` + a dev restart
  to appear (the running binary embeds a stale bundle otherwise).
- 2026-07-01T15:30:00+08:00 user asked how to accept a time/epoch-driven spec; chose to extend
  the devflow harness. Added p3-6 (TC-14): `cmd/devflow` section 3 narrates the full lifecycle
  via the real reconciler in seconds. Acceptance aid, dev-only (not product code).
- 2026-07-01T15:10:00+08:00 multi-agent review (claude+cursor APPROVE, no P0/P1; codex hit
  usage limit). User approved fixing review P2-1/2/3/5 + P3-6 → appended as p3-1..p3-5 (TC-9..TC-13).
  P2-4 (sync notification) and the design-tradeoff items left as-is.
- 2026-07-01T15:16:00+08:00 p3-1..p3-5 delivered + committed; TC-9..TC-13 evidence appended;
  full regression green. TC-6 upgraded from partially-met to fully met (p3-3 automated gate test).
  Spec still held OPEN pending user verification.
