# Subscription lifecycle: sliding expiry + grace + expiry notifications + tier refresh

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
  `active` keeps an empty/stale tier until they re-bind (this is the second half of the
  empty-tier issue whose first half — the Koios `pool_id_bech32` parse — was fixed in
  S0018 p2-1).

We also validated the membership timing model (entry/leaving each lag ~2 epochs; the
leaving tail keeps a departed delegator `active` for ~2 epochs; a returning delegator
passes through a `pending` window). `pending` is treated as a member — good UX, but it
grants **base membership** to dust/Sybil delegators (it can never grant a tier: tier
needs real active stake, which is unfakeable). The chosen policy (option 1) is to gate
all value on **tier**, not on mere membership — which the platform already supports
(push `TargetTier`, tier claim in tokens) and which relies on tier being accurate.

### Scope

- **Sliding expiry (approach B):** each successful per-epoch re-verify of a member
  (`active`/`pending`) extends `ExpiresAt = now + TTL`. Time-based expiry only bites when
  the reconciler cannot confirm membership for the whole window.
- **Grace + terminal expiry on membership loss:** when the reconciler first sees
  `state == none` for an active subscription, it shortens `ExpiresAt` to `now + GRACE`
  (short) and notifies the user, rather than expiring instantly. If the member recovers
  (`pending`/`active`) before `ExpiresAt`, the session is restored (`ExpiresAt` back to
  `+TTL`, seamless — no re-bind). If still `none` past `ExpiresAt`, `status=expired`.
- **Enforce `ExpiresAt`:** the reconciler expires any active session past `ExpiresAt`.
- **Tier refresh:** switch the reconciler from `Membership` (state) to `Attest`
  (state+tier) and write the fresh `tier` back each epoch, so `pending→active` upgrades
  and stake-driven tier changes are reflected automatically.
- **Expiry notification:** a bot DM to the affected user when a subscription enters grace
  (membership lost), reusing the push transport routing.
- **`/status` truthfulness:** show the real `ExpiresAt` and `LastVerifiedAt`.
- **Policy doc (option 1):** document that value must be gated on tier (not membership);
  `pending`/dust get base membership but no tier, so tier-targeted pushes / tier-checking
  relying parties are the correct gate.

### Constraints

- No change to `DeriveState` / eligibility semantics or the Koios adapter — this is the
  subscription lifecycle + tier bookkeeping layer only.
- The reconciler stays **fail-open** (D8): a chain query error keeps the session and does
  NOT notify (only a definitive `state == none` triggers grace/notify) — never spam users
  on a transient Koios hiccup.
- Notifications are best-effort: a send failure is logged, never blocks reconcile, and the
  warning is sent **once** on grace-entry (not every epoch).
- `GRACE` must cover the "membership lost → re-delegation's `pending` observed by the next
  reconcile" window (≈1 epoch), else auto-recovery breaks. Default sized to ≥2 mainnet
  epochs.

### Non-goals

- Admin-configurable TTL/GRACE (use consts this spec; admin config is a future option).
- Notifying on chain-unreachable / operational time-expiry (only membership-loss notifies
  here; the operational path still expires but silently — future enhancement).
- Non-telegram channel notifications (telegram only now; the notifier is channel-typed so
  it can extend later).
- Enforcing option 1 in code (the "defense-in-depth" hardening below is optional).

## 2. Outline Design

- **`reconciliation.StateEvaluator`**: change the method from
  `Membership(ctx, sch) (State, error)` to `Attest(ctx, sch) (State, string, error)`
  (the `oauth.Server` already implements both). `Reconcile` uses the returned `tier`.
- **`Reconcile` per active session** (`reconciliation.go`):
  - `Attest` errors → fail-open (keep, count Failed, no notify) — unchanged.
  - member (`active`/`pending`): `LastVerifiedAt = now`; `Tier = tier`;
    `ExpiresAt = now + TTL`; if it was previously in grace, that clears (restored).
  - `state == none`:
    - if not already in grace (i.e. `ExpiresAt` still far / `> now + GRACE`): set
      `ExpiresAt = now + GRACE`, **send the grace notification once**.
    - else (grace already running): if `now >= ExpiresAt` → `status = expired`
      (optionally a final DM); else keep (still in grace).
  - "in grace" is detected from `ExpiresAt` proximity (a member pass always pushes it to
    `now+TTL`, so `ExpiresAt <= now+GRACE` uniquely marks grace) — or a small explicit
    marker if that proves ambiguous (decide in impl).
- **Notifier seam**: `reconciliation.New(...)` gains a `Notifier` dependency, e.g.
  `func(ctx, sess domain.SubscriptionSession, msg string) error`. `main.go` wires it to
  resolve the session's channel instance token (`instanceToken`) + `telegram.NewBotAPITransport`
  and `SendMessage(channel_user_id, msg)` — the same routing the push worker uses. nil
  Notifier → notifications skipped (tests / degraded).
- **`/status`** (`telegram.go`): already shows `ExpiresAt`; add `LastVerifiedAt` and make
  the wording reflect the sliding/grace meaning.
- **Docs**: `docs/multi-channel-instances.md` (or a subscriptions doc) records the
  lifecycle (sliding TTL, grace, notify, tier refresh) and the option-1 policy (value on
  tier, `pending`/dust = base membership only).

### Risk and rollback

- Risk: enforcing `ExpiresAt` for the first time could expire sessions that were silently
  living past a stale 30-day date. Mitigation: the reconciler pushes `ExpiresAt` to
  `now+TTL` for every current member on its first pass, so members are re-anchored before
  any enforcement bites; only genuine non-members expire. Rollback = git revert.
- Risk: notification plumbing errors. Mitigation: best-effort, fail-open, logged.

## References

- `internal/worker/reconciliation/reconciliation.go`, `internal/core/oauth/oauth.go`
  (`Membership`/`Attest`/`attest`/`firstPartyTier`), `internal/worker/telegram/telegram.go`
  (`sessionTTL`, `/status`, activate), `internal/worker/push/push.go` +
  `cmd/issuer/main.go` (`instanceToken`, transport routing), `internal/store/repo_subscriptionsession.go`.
  Prior: S0018 p2-1 (Koios `pool_id_bech32` fix — the other half of accurate tier).

## 3. Execution Plan

- [ ] p1-1 Reconciler refreshes tier: switch `StateEvaluator` to `Attest` (state+tier);
      write `Tier` back on each member re-verify. Update reconcile tests (mock returns tier;
      assert `pending→active` upgrades the stored tier).
- [ ] p1-2 Sliding expiry + grace + enforcement: member → `ExpiresAt = now + TTL`;
      first `none` → `ExpiresAt = now + GRACE`; recovery restores; `now >= ExpiresAt` while
      `none` → `status=expired`. Consts `subscriptionTTL`, `subscriptionGrace`. Tests cover
      extend / grace-entry / restore-within-grace / expire-after-grace.
- [ ] p1-3 Expiry notification: `Notifier` seam on the reconciler; `main.go` wires the
      telegram transport (per-instance token routing); send the grace warning once on
      grace-entry; best-effort + fail-open. Test the seam is called once with the right
      session/message and that send errors don't fail reconcile.
- [ ] p1-4 `/status` + docs: show real `ExpiresAt` + `LastVerifiedAt`; document the
      lifecycle and the option-1 policy (value gated on tier; `pending`/dust = base only).
- [ ] p2-1 Validation: `make test` + `pnpm test` + `shellcheck deploy/install.sh`.
- [ ] p3-1 (OPTIONAL, defense-in-depth) Enforce option 1 in code: admin push UI
      requires/defaults a target tier, and/or the backend drops untargeted broadcasts to
      tier-less subscriptions. Decide whether to do now or defer.

## 4. Test and Acceptance Criteria

- TC-1 Tier refresh: a subscription created while `pending` (empty tier) has its stored
  `tier` set to the correct value once the credential becomes `active` at the next
  reconcile — no re-bind required. `Membership`→`Attest` swap verified.
- TC-2 Sliding expiry: each reconcile of a still-member session advances `ExpiresAt` to
  `now + TTL`; a member is never expired while membership holds.
- TC-3 Grace on loss: first `state == none` shortens `ExpiresAt` to `now + GRACE` and does
  not expire; a recovery to `pending`/`active` before `ExpiresAt` restores it (`+TTL`, no
  re-bind); still `none` past `ExpiresAt` → `status = expired`.
- TC-4 Fail-open: a chain/`Attest` error keeps the session, does not shorten `ExpiresAt`,
  and sends no notification.
- TC-5 Notification: entering grace triggers exactly one bot DM to the session's user via
  the correct instance transport; a send error is logged and does not fail the pass; no DM
  on subsequent still-`none` epochs.
- TC-6 `/status`: shows the real `ExpiresAt` and `LastVerifiedAt`.
- TC-7 Regression: `make test` + `pnpm test` green; `shellcheck deploy/install.sh` clean.

Pass/fail: TC-1..TC-7 pass; no change to `DeriveState`/eligibility/Koios semantics.

## 5. Execution Log (append-only)

- 2026-07-01T11:52:34+08:00 spec drafted (S0019) after the subscription-lifecycle design
  discussion. Approach B (sliding `ExpiresAt`) + grace-on-loss + one-shot expiry
  notification + reconciler tier refresh (`Membership`→`Attest`); option-1 freeload policy
  documented (value gated on tier). DEFAULTS TAKEN (editable before execution): notify only
  on membership loss (not operational time-expiry); `subscriptionTTL = 30d`,
  `subscriptionGrace ≈ 14d` (≥2 mainnet epochs, must cover the pending re-observation
  window); consts (no admin config); defense-in-depth (p3-1) listed OPTIONAL. Open
  decisions for review: (1) notify scope, (2) TTL/GRACE values, (3) do p3-1 now or defer.

## 6. Validation Evidence (append-only)

## 7. Change Requests (append-only)
