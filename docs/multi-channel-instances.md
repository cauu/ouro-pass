# Multi-channel / multi-instance channels (S0005)

This document describes the channel-instance model introduced in S0005. It builds
on the staking-attestation core ([`staking-attestation.md`](staking-attestation.md))
and on-chain credentials ([`onchain-credentials.md`](onchain-credentials.md)) — it
changes **nothing** about attestation, tokens, or `tier_rules`. It changes how the
issuer delivers memberships: a channel **instance** is now a first-class,
addressable entity, end to end through worker / subscription / activation / push /
admin UI.

## 1. What changed

Before S0005 the model was *one pool, one `channel_type`, one instance*: everything
addressed channels by **type** (`GetByType("telegram")` → a single row), and a pool
could run exactly one Telegram bot.

After S0005 a pool may run **N active instances of one platform** (e.g. a members
bot + an announcements bot, or per-tier bots). Each instance has:

- a **stable `channel_id`** (generated at creation, never changes on save),
- a human-readable **`name`**, unique within a `(pool_id, channel_type)`,
- its own **encrypted bot token** and its own **worker** (independent long-poll
  offset),
- its own **public `bot_username`** (stored in clear) for activation deep links.

> Scope this release: the model and admin surface are multi-platform-ready
> (`channel_type` is general), but the only runtime transport delivered is
> **telegram**. New platforms (discord/slack/…) are separate future work.

## 2. Data model

| Table | S0005 change |
|---|---|
| `ChannelConfig` | `+ name TEXT`; `UNIQUE(pool_id, channel_type, name)`; `channel_id` is the stable instance id |
| `SubscriptionSession` | `+ channel_id`; unique key moved `(pool_id, channel_type, channel_user_id)` → **`(channel_id, channel_user_id)`** so one channel user may subscribe to several instances independently |
| `ActivationCode` | `+ channel_id` — the instance a code binds to |
| `PushJob` | `+ channel_id` (nullable) — target a single instance, else legacy type-level fan-out |

Migration `0014_channel_instances` backfills existing single-telegram data to a
`default` instance; `0015_pushjob_channel` adds the nullable push target.
`OUROPASS_TELEGRAM_TOKEN` remains an implicit "default" instance (see §6).

## 3. Worker supervisor (`internal/worker/telegram/supervisor.go`)

A single **supervisor** goroutine owns the running set of per-instance workers and
reconciles it against the active-instance set every tick:

```
reconcile every interval:
  want = Channels.ListActive(pool, "telegram")        // desired set (by channel_id)
  for running id not in want OR fingerprint changed:  cancel() + drop  (logs "worker stopped")
  for want id not running:                            factory(inst) → start child ctx  (logs "worker started")
```

- **Single owner** of the running map → an instance is never run twice (C4).
- **Child context** per worker → `cancel()` propagates; a removed/disabled instance's
  worker exits within one tick; all workers drain on shutdown (no goroutine leak).
- **Fingerprint** = `status | updated_at`: an admin editing a token live changes the
  fingerprint, so the supervisor stops the old worker and starts a fresh one — **no
  process restart**.
- The `Factory` decrypts the instance token and builds a transport + an
  **instance-scoped** `Processor` (binds subscriptions to `channel_id`, looks members
  up by `(channel_id, channel_user_id)`).

## 4. Activation binds an instance

`CreateActivation(channel_id, …)` records the target instance on the code and builds
the deep link to **that instance's** bot (`https://t.me/<bot_username>?start=<code>`).
On `/start`, `ActivationCode.Consume(code, channelType, channelID, …)` rejects a code
bound to a different instance (`ErrPurpose`), so an instance's bot never redeems
another instance's code. The processor writes `subscription.channel_id`. Unbound
legacy codes (`channel_id == ""`) stay type-scoped for back-compat.

## 5. Push binds an instance

`PushJob.channel_id` (optional) scopes a job to one instance: the scheduler selects
`ListActiveByInstance(channel_id)` and routes the send through **that instance's**
transport (`Options.Route`). A routing failure (e.g. an unconfigured token) fails the
job rather than misdelivering. An unscoped job keeps the legacy type-level fan-out.

## 6. Lifecycle & semantics (decisions)

- **Create / update / enable-disable / delete** via `POST/DELETE /api/admin/channels[/{id}]`
  (operator role, audited). Tokens are encrypted at rest; `bot_username` is public.
- **Delete cascade (D7):** deleting (or you may instead disable) an instance cancels
  its active subscriptions (`CancelByChannelID`).
- **Env fallback (D6/C1):** when `OUROPASS_TELEGRAM_TOKEN` is set and **no** DB
  instance exists, the supervisor runs an implicit `default` instance; it stops as soon
  as a DB instance is configured.
- **Out of scope:** cross-instance member dedup (the same stake subscribing on two bots
  is two independent subscriptions); per-instance RBAC (operator role gates all
  instances).

## 7. Key files

- `internal/store/repo_channelconfig.go` — instance CRUD by id
- `internal/store/repo_subscriptionsession.go` — `GetByInstanceUser` / `ListActiveByInstance` / `CancelByChannelID`
- `internal/worker/telegram/supervisor.go` — the reconciling supervisor
- `internal/worker/telegram/telegram.go` — `NewInstanceProcessor` (channel_id-aware)
- `internal/worker/push/push.go` — `Options.Route` per-instance delivery
- `internal/httpapi/handlers_admin_resources.go` — channels CRUD endpoints
- `cmd/issuer/main.go` — supervisor + push-route wiring, `instanceToken`
- `web/src/features/channels/ChannelsPage.tsx` — N-instance admin UI
