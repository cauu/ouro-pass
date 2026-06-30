# Bind page targets a specific channel instance (per-channel deep link)

Spec-ID: S0016
Status: active
Created Time: 2026-06-30T17:48:05+08:00
Start Time: 2026-06-30T19:37:14+08:00
Completion Time:
Previous Spec-ID: S0015
Closure Reason:

## 1. Requirement Details

### Background

The public `/bind` page lets a holder prove staking identity and receive a Telegram
activation deep link (`https://t.me/<bot>?start=<code>`). The per-channel bot feature
already exists server-side: `activationCreate` (`handlers_activation.go:35-45`) uses a
selected instance's own `bot_username` (S0005 p2-2) **when `channel_id` is provided**,
else falls back to the deployment-wide `OUROPASS_TELEGRAM_BOT` env default.

But that channel-selection path is **dead for the public bind page**:

- `/bind` (`handlers_oauth.go:49-61`) only reads `channel_type`; it never accepts or
  propagates a `channel_id`.
- The bind JS (`authpage/assets/ouropass-auth.js:145-155`) submits activation with only
  `channel_type` — never `channel_id`.
- So the server always takes the fallback branch and returns the env-default bot.

**Observed:** after a successful wallet verification the deep link pointed at
`https://t.me/ouro_dev_bot?start=…` (the `make dev` `OUROPASS_TELEGRAM_BOT` default),
not the bot of the channel instance configured in the admin console — the configured
channels are never consulted.

**Downstream symptom (same root cause):** the Subscription/Member admin pages stay
empty after binding. A `SubscriptionSession` row is only written when the bot worker
processes `/start <code>` (`telegram.go:107-141`). But once an admin adds a DB telegram
instance, the env-default (`ouro_dev_bot`) worker is no longer started — the supervisor
runs the env fallback only while no DB instance exists (`supervisor.go:83-86`). The
deep link points at `ouro_dev_bot`, which has no running worker, so `/start` is never
processed, no subscription is created, and the Member page (= subscriptions,
`handlers_admin_resources.go:99`) shows nothing. (The activation code is unbound —
`channel_id==""` — so `Consume`'s scoping allows any telegram instance to redeem it
(`repo_activationcode.go:53`); the only thing missing is a *running* worker for the
linked bot — which the explicit-`channel_id` fix provides.)

### Scope

Add an **explicit** per-channel bind path (operator decision — explicit `channel_id`
only, no server-side auto-selection of a "single" instance):

- `/bind?channel_id=<id>` renders a page bound to that instance; the activation request
  carries `channel_id`, so the deep link uses that instance's `bot_username`.
- `/bind` with **no** `channel_id` keeps current behavior unchanged (env-default bot).
- The admin **Channels** page surfaces a copyable per-channel bind link
  (`<origin>/bind?channel_id=<id>`) for each Telegram instance.

### Constraints

- No change to eligibility/activation semantics — only which bot username the deep link
  uses, driven by an already-existing server branch.
- `channel_id` must be validated as an **active telegram** instance; an unknown/inactive
  id must fail clearly (no silent fall-through to the env default, which would hide a
  misconfigured link).
- Same-origin only; the bind link uses the page origin, no new config.
- CSP unchanged (data-* attributes only, no inline script).

### Non-goals

- Server-side auto-selection of "the single active instance" (explicitly declined).
- Multi-channel pickers / channel chooser UI on the bind page.
- Changing the env-default ("default" instance) behavior for `/bind` with no id.

## 2. Outline Design

- **`handlers_oauth.go` `bind`**: read `channel_id` query param. If present, look it up
  (`Store.Channels().Get`) and require `status=="active" && channel_type=="telegram"`;
  on failure return `400 invalid_request` (do not render with a bad id). Pass the id into
  `BindData`.
- **`authpage.BindData`**: add `ChannelID string`.
- **`templates/bind.html`**: add `data-channel-id="{{.ChannelID}}"` on `#op-app`.
- **`authpage/assets/ouropass-auth.js`** `submitActivation`: include
  `channel_id: cfg.channelId || ""` in the POST. Empty → omitted semantics preserved
  (server already treats `""` as "no instance").
- **`activationCreate`**: unchanged — it already resolves the instance username when
  `channel_id != ""`.
- **Admin web `ChannelsPage.tsx`**: for each `telegram` instance add a "Copy bind link"
  action that copies `${window.location.origin}/bind?channel_id=<channel_id>`.

### Risk and rollback

- Risk: validating `channel_id` in `/bind` adds a store read on a public route. It is
  already rate-limited (`publicLimit`); a single indexed PK lookup is cheap. Rollback =
  git revert.
- Risk: an existing bookmarked `/bind` (no id) still works (env default) — no regression.

## References

- `server/internal/httpapi/handlers_activation.go` (per-channel username branch),
  `handlers_oauth.go` (`bind`), `authpage/` (template + JS),
  `worker/telegram/channelconfig.go` (`DecodeUsername`).
- `web/src/features/channels/ChannelsPage.tsx`, `web/src/lib/types.ts`.
- S0005 (channel instances / per-instance bot). Memory: [[installer-scope-boundary]].

## 3. Execution Plan

- [x] p1-1 Backend bind path: `/bind` accepts + validates `channel_id` (active telegram),
      propagates via `BindData.ChannelID` → `data-channel-id`; JS sends `channel_id` in the
      activation POST. Unit-test the handler (valid id → data-channel-id rendered; bad id →
      400; no id → unchanged).
- [x] p1-2 Admin web: per-Telegram-instance "Copy bind link"
      (`<origin>/bind?channel_id=<id>`) in `ChannelsPage`.
- [x] p1-3 Validation: `make test` + `pnpm test` (+ a focused activation test asserting the
      instance `bot_username` lands in the deep link when `channel_id` is passed).

## 4. Test and Acceptance Criteria

- TC-1 Per-channel deep link: `/bind?channel_id=<active tg id>` renders `data-channel-id`,
  and `POST /api/activation/create` with that `channel_id` returns a deep link using the
  instance's `bot_username` (not the env default).
- TC-2 Backwards-compatible: `/bind` with no `channel_id` renders with empty
  `data-channel-id`; activation falls back to `OUROPASS_TELEGRAM_BOT` (unchanged).
- TC-3 Bad id rejected: `/bind?channel_id=<unknown|inactive|non-telegram>` → `400`, no
  deep link with the env-default bot.
- TC-4 Admin UX: `ChannelsPage` shows a copyable `<origin>/bind?channel_id=<id>` link for
  each Telegram instance.
- TC-5 Regression: `make test` + `pnpm test` green.
- TC-6 End-to-end (manual/dev): binding via `/bind?channel_id=<configured tg id>` yields a
  deep link to the configured bot whose worker is running; opening it and sending `/start
  <code>` creates a subscription, so the admin **Member/Subscription** page shows the new
  row (resolves the "empty pages" symptom). Pre-fix confirmation: opening the *configured*
  bot with the same `?start=<code>` already creates the subscription (proves the only defect
  is the deep-link bot username).

Pass/fail: TC-1..TC-6 pass; no change to eligibility/activation semantics.

## 5. Execution Log (append-only)

- 2026-06-30T17:48:05+08:00 spec drafted (S0016) after S0015 verification surfaced that the
  public /bind deep link ignores admin-configured channels and returns the env-default bot.
  Operator chose explicit channel_id only (no auto-select). Awaiting promotion to active
  (blocked until S0015 is closed — only one active spec at a time).
- 2026-06-30T19:37:14+08:00 S0015 closed (delivered); promoted S0016 to active (Start Time set;
  file moved to docs/specs/). Beginning execution of p1-1.
- 2026-06-30T19:44:00+08:00 p1-1: bind handler reads + validates channel_id (active telegram
  instance, else 400) and passes it via BindData.ChannelID → data-channel-id in bind.html;
  ouropass-auth.js now sends channel_id in the activation POST. activationCreate unchanged (it
  already resolves the instance username when channel_id != ""). oauthDeps test helper now wires
  Store/Cipher/TelegramBot. Added TestBind_ChannelID (valid→data-channel-id, no-id→empty attr,
  unknown/disabled→400).
- 2026-06-30T19:50:00+08:00 p1-2: ChannelsPage adds a per-Telegram-instance "Copy bind link"
  action that copies `${window.location.origin}/bind?channel_id=<id>` to the clipboard (toast
  feedback). UI-only; covered by typecheck + lint + build (and pnpm test in p1-3).
- 2026-06-30T19:52:00+08:00 p1-3: added TestActivationCreate_UsesInstanceBot (channel_id →
  deep link uses instance bot `my_real_bot`; no id → `ouro_default_bot` fallback). Full
  validation: go vet clean, make test green, pnpm test (web 10/10) green, typecheck clean.
  All plan items p1-1..p1-3 complete & verified; spec left active pending user verification
  (do not close).

## 6. Validation Evidence (append-only)
- TC-1/TC-2/TC-3 | stack: go | command: go test ./internal/httpapi/ -run TestBind_ChannelID | result: pass | note: /bind?channel_id=<active tg> renders data-channel-id; no id → empty attr (env-default fallback); unknown/disabled → 400
- TC-4 | stack: ui | command: npm run typecheck && npx eslint ChannelsPage.tsx | result: pass | note: per-telegram "Copy bind link" → <origin>/bind?channel_id=<id>; typecheck + lint clean
- TC-1/TC-2 | stack: go | command: go test ./internal/httpapi/ -run TestActivationCreate_UsesInstanceBot | result: pass | note: channel_id → deep link t.me/my_real_bot; no id → t.me/ouro_default_bot (env-default fallback)
- TC-5 | stack: go | command: go vet ./... && make test | result: pass | note: vet clean; full server suite green
- TC-5 | stack: node | command: pnpm test (web) + npm run typecheck | result: pass | note: vitest 10/10; tsc clean
- TC-6 | stack: manual | command: open /bind?channel_id=<configured tg> → /start <code> | result: pending | note: end-to-end (subscription appears in Member page) is the user's manual verification step

## 7. Change Requests (append-only)
