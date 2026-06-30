# Bind page: public channel directory + in-page channel selection

Spec-ID: S0018
Status: active
Created Time: 2026-06-30T23:10:16+08:00
Start Time: 2026-06-30T23:23:05+08:00
Completion Time:
Previous Spec-ID: S0017
Closure Reason:

## 1. Requirement Details

### Background

After S0016/S0017 the activation deep link is correct only when the bind page is
opened with `?channel_id=<id>`: `/bind` with no id renders a page whose activation
then 400s ("channel_id is required"). The per-channel link works (admin's "Copy bind
link"), but a holder visiting a bare `/bind` has no way to discover or pick a channel.

Desired flow: the bind page should first let the holder **choose which channel to
subscribe to**, then run the existing connect → sign → activation → deep-link flow
bound to that channel.

The blocker is that the channel list is only exposed under `/api/admin/channels`
(owner session + RBAC); the public bind page cannot read it. So this spec adds a
public, read-only channel directory and a selection step (interaction **plan A**:
a directory entry page that links to the existing per-channel bind page).

### Scope

- New **public, read-only** endpoint `GET /api/channels` returning active channel
  instances with public fields only (`channel_id`, `name`, `channel_type`,
  `bot_username`) — never the token or token hint.
- `/bind` with no `channel_id` becomes a **channel directory**: it lists the channels
  and each entry navigates to `/bind?channel_id=<id>` (the existing S0016 wallet/sign/
  activation flow). 0 channels → an explanatory message; exactly 1 → auto-advance to
  that channel; 2+ → the list.
- The per-channel bind page shows which channel was selected (name + `@bot_username`)
  and a "← choose a different channel" link back to `/bind`.

### Constraints

- Public endpoint: rate-limited (`publicLimit`), active instances only, public fields
  only. `channel_id` is already public (it appears in the bind URL since S0016), so
  exposing it is harmless; activation still requires a valid wallet signature +
  eligibility.
- Only list instances that are usable for binding: active, `channel_type == "telegram"`,
  and carrying a non-empty `bot_username` (so the directory never offers a dead entry).
- CSP unchanged: same-origin `fetch('/api/channels')` is already allowed by
  `connect-src 'self'`; no inline script (data-* + the existing same-origin asset).
- No change to eligibility/activation semantics; the per-channel flow is reused as-is.

### Non-goals

- A per-instance `listed/unlisted` flag (this spec lists all active telegram
  instances). Hiding internal bots from the directory is a future enhancement; direct
  `/bind?channel_id=` links already bypass any directory.
- A `description` field for richer directory cards (DB change; future).
- Non-telegram channel types (the payload carries `channel_type` so the UI can
  generalize later, but only telegram is rendered now).
- A single-page wizard (selection and signing stay as separate, single-purpose pages).

## 2. Outline Design

- **`internal/httpapi`**: add `publicChannels` handler returning
  `{channels: [{channel_id, name, channel_type, bot_username}]}` from
  `Store.Channels().ListActive(ctx, PoolID, "telegram")`, decoding `bot_username` via
  `telegram.DecodeUsername` and dropping entries with an empty username. Register
  `r.With(publicLimit).Get("/api/channels", h.publicChannels)`.
- **`authpage`**: `bind.html` gains a directory container (`#op-channels`) shown when
  no `channel_id` is set; `ouropass-auth.js` adds a "select" branch — when
  `cfg.channelId` is empty it `fetch`es `/api/channels`, then:
  - 0 → render an "ask the operator" message;
  - 1 → `location.replace('/bind?channel_id=' + the one id)`;
  - 2+ → render a button per channel (`name` + `@bot_username`) linking to
    `/bind?channel_id=<id>`.
  When `cfg.channelId` is set, it runs the existing activate flow unchanged.
- **`bind` handler / `BindData`**: keep the S0016 validation; when `channel_id` is
  present, pass the instance `name` + `bot_username` into `BindData` →
  `data-channel-name` / `data-channel-bot` so the per-channel page can show "Subscribing
  to <name> (@bot)" plus a back link to `/bind`.

### Risk and rollback

- Risk: the channel directory is public. Mitigation: only public fields, rate-limited,
  and `channel_id`/`bot_username` are already public. If an operator runs an internal
  bot they don't want listed, that's the future `listed` flag (non-goal) — for now a
  direct link still works and the directory lists all active bots. Rollback = git revert.
- Risk: `/bind` behavior changes from "renders a form that 400s" to "directory" —
  strictly an improvement; `/bind?channel_id=` links are unaffected.

## References

- `internal/httpapi/handlers_oauth.go` (`bind`), `handlers_activation.go`, `router.go`,
  `authpage/templates/bind.html`, `authpage/assets/ouropass-auth.js`,
  `store/repo_channelconfig.go` (`ListActive`), `worker/telegram/channelconfig.go`
  (`DecodeUsername`). Prior: S0016 (per-channel deep link), S0017 (admin-only telegram).

## 3. Execution Plan

- [x] p1-1 Public channel directory endpoint: `GET /api/channels` (active telegram
      instances with a bot username; public fields only). Handler + router + unit test
      (active-with-username listed; inactive / no-username / token excluded).
- [ ] p1-2 Bind directory UX: `/bind` (no `channel_id`) fetches `/api/channels` and
      renders a picker (0 → message, 1 → auto-advance, 2+ → links to
      `/bind?channel_id=<id>`); per-channel page shows the selected channel
      (`name`/`@bot`) + a back-to-directory link (`BindData` gains name/bot). Update
      `bind.html`, `ouropass-auth.js`, `bind` handler.
- [ ] p1-3 Validation: `make test` + `pnpm test` + `shellcheck deploy/install.sh`
      (+ handler test for the endpoint and an asset check for the directory branch).

## 4. Test and Acceptance Criteria

- TC-1 Public list: `GET /api/channels` returns active telegram instances that have a
  `bot_username`, with only `channel_id`/`name`/`channel_type`/`bot_username` (no token /
  token hint); inactive, non-telegram, and username-less instances are excluded.
- TC-2 Directory: `/bind` with no `channel_id` presents the channels — 0 → an
  explanatory message, exactly 1 → auto-advance to `/bind?channel_id=<that>`, 2+ → a
  link per channel to `/bind?channel_id=<id>`.
- TC-3 Per-channel page: `/bind?channel_id=<active tg>` shows the selected channel
  (`name` + `@bot_username`) and a back link to `/bind`; connect → sign → activation →
  deep link is unchanged (S0016).
- TC-4 Regression: `make test` + `pnpm test` green; `shellcheck deploy/install.sh` clean.

Pass/fail: TC-1..TC-4 pass; no change to eligibility/activation/deep-link semantics.

## 5. Execution Log (append-only)

- 2026-06-30T23:10:16+08:00 spec drafted (S0018): add a public channel directory and an
  in-page selection step so a bare `/bind` lets holders pick a channel before the
  connect → sign → activation flow (interaction plan A). Defaults: list all active
  telegram instances (no listed/unlisted flag — future), auto-advance on a single
  channel, telegram-only rendering. Blocked on promotion until S0017 is closed (one
  active spec at a time).
- 2026-06-30T23:23:05+08:00 S0017 closed (delivered); promoted S0018 to active (Start Time set;
  file moved to docs/specs/). Beginning execution of p1-1.
- 2026-06-30T23:28:00+08:00 p1-1: added GET /api/channels (public, rate-limited) →
  {channels:[{channel_id,name,channel_type,bot_username}]} from Channels().ListActive(pool,
  "telegram"), dropping instances with no decodable bot_username; only public fields, no token.
  New handlers_channels.go + route in router.go. TestPublicChannels asserts active+username
  listed; disabled / no-username excluded; no token leak.

## 6. Validation Evidence (append-only)
- TC-1 | stack: go | command: go test ./internal/httpapi/ -run TestPublicChannels | result: pass | note: /api/channels lists active telegram w/ username; disabled & username-less excluded; no bot_token_enc/token_hint in payload

## 7. Change Requests (append-only)
