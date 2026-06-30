# Retire the env-default Telegram instance — all bots configured in admin

Spec-ID: S0017
Status: active
Created Time: 2026-06-30T20:52:15+08:00
Start Time: 2026-06-30T20:58:08+08:00
Completion Time:
Previous Spec-ID: S0016
Closure Reason:

## 1. Requirement Details

### Background

Telegram delivery has two parallel configuration paths:

- **Admin (DB) instances** — created in the admin console; each stores its own
  encrypted bot token **and** public `bot_username` (`worker/telegram/channelconfig.go`).
  This is the real, multi-bot path (S0005).
- **env-default** — a synthetic, in-memory instance (`envInstanceID = "env-default"`,
  `main.go:46-48,155-161`) created from `OUROPASS_TELEGRAM_TOKEN`, plus a separate
  `OUROPASS_TELEGRAM_BOT` that supplies the deep-link **username** (the token alone
  doesn't contain it). It runs only while **no** DB instance exists
  (`supervisor.go:83-86`) and otherwise steps aside.

This env path is a legacy bootstrap convenience that is now a footgun and conflicts
with the project's own boundary ([[installer-scope-boundary]]: Telegram is an
admin/DB entity, not deploy-time env):

- The two vars are decoupled and easy to half-configure. `make dev` sets only
  `OUROPASS_TELEGRAM_BOT=ouro_dev_bot` (no token), so the deep-link fallback points
  at a bot with **no running worker** — a dead link (the S0016 symptom: empty
  Member/Subscription pages because `/start` is never processed).
- The deep-link fallback to `OUROPASS_TELEGRAM_BOT` silently hides a misconfigured
  link instead of failing.
- There is no chicken-and-egg: the admin console is reached via the **owner wallet**,
  independent of Telegram, so an operator can always configure the first bot in admin.

Operator decision: **retire env-default and both telegram env vars**. All Telegram
bots are configured in admin (token + username stored there); deep links use only the
per-channel stored username, with **no env fallback**.

### Scope

- Remove `OUROPASS_TELEGRAM_TOKEN` and `OUROPASS_TELEGRAM_BOT` from config/env/docs.
- Remove the synthetic `env-default` instance and all its wiring (supervisor fallback,
  `instanceToken` env branch, push default-token env branch, `Deps.TelegramBot`).
- Activation deep links resolve the bot username **only** from the targeted channel
  instance; if there is no `channel_id`, or the instance has no `bot_username`, the
  request fails (400) instead of falling back to an env-default bot.
- Update `.env.example`, `docs/deployment.md` (incl. the stale `OURO_TELEGRAM_*`
  installer mention at `deployment.md:54`), and `cmd/devflow` / tests accordingly.

### Constraints

- No change to the admin (DB) channel path: token encryption, username storage,
  per-instance workers, supervisor reconcile, push routing all stay — only the env
  fallback layer is removed.
- **Breaking for env-token deployments**: a deploy currently relying on
  `OUROPASS_TELEGRAM_TOKEN`/`_BOT` loses its bot on upgrade until the operator adds a
  Telegram instance in admin. Must be documented as a migration step.
- `make test` + `pnpm test` green; `shellcheck deploy/install.sh` clean (installer
  already doesn't touch Telegram).

### Non-goals

- Deriving the bot username from the token via Telegram `getMe` (possible future
  admin convenience; out of scope — admin already captures the username on create).
- Auto-selecting a single DB instance when `channel_id` is omitted (explicit-only,
  per S0016).
- Non-telegram channels (discord/email/webhook).

## 2. Outline Design

- **`config.go`**: drop `TelegramBot` + `TelegramToken` fields and their env reads;
  optionally log a one-line deprecation note if a legacy var is still set (like the
  S0015 chain-source pattern). Update `config_test`.
- **`cmd/issuer/main.go`**: delete `envInstanceID`, the `envInstance` construction, and
  pass nothing for it to the supervisor; remove the `cfg.TelegramToken` branch in
  `pushDefaultTokenFn` (unscoped push resolves the default token from the DB telegram
  instance via `Channels().GetByType`); simplify `instanceToken` to always decrypt the
  instance's own stored token (drop the `envInstanceID` special case); remove
  `deps.TelegramBot`.
- **`worker/telegram/supervisor.go`**: drop the `envInstance` field + `NewSupervisor`
  param + the `len(out)==0` fallback in `desired()`. Update `supervisor_test`.
- **`httpapi` `Deps`/`router.go`**: remove the `TelegramBot` field.
- **`handlers_activation.go`**: resolve `botUsername` only from the instance
  (`DecodeUsername`); require `channel_id`, and 400 if the instance has no username — no
  env fallback. (`bind` already validates `channel_id`; with no fallback, a bare `/bind`
  with no `channel_id` yields a 400 at activation — optionally also reject at `/bind`.)
- **`cmd/devflow/main.go`**: drop the `TelegramBot` Dep it sets.
- **`.env.example` / `docs/deployment.md`**: remove both telegram env vars and the stale
  `OURO_TELEGRAM_*` installer line; document "Telegram bots are configured in admin
  (/admin → Channels); each instance carries its own token + username" + a migration
  note for existing env-token deployments.

### Risk and rollback

- Risk: existing env-token deploys lose Telegram until reconfigured in admin.
  Mitigation: BREAKING migration note + a boot deprecation log naming the ignored vars.
  Rollback = git revert.
- Risk: unscoped (legacy) push jobs previously could use the env token. After this they
  resolve the default DB telegram instance's token; if none exists, the job has no
  sender (same as today when env token is unset). Covered by push worker tests.

## References

- `server/cmd/issuer/main.go` (env-default wiring), `worker/telegram/supervisor.go`,
  `internal/httpapi/handlers_activation.go` / `router.go`, `internal/config/config.go`,
  `cmd/devflow/main.go`. Memory: [[installer-scope-boundary]]. Prior: S0016 (per-channel
  deep link), S0005 (channel instances).

## 3. Execution Plan

- [ ] p1-1 Config: remove `TelegramBot` + `TelegramToken` fields + env reads; legacy
      values logged-and-ignored. Update `config_test`.
- [x] p1-2 Issuer wiring: delete the `env-default` instance, drop the supervisor
      `envInstance` param + `desired()` fallback, simplify `instanceToken`, make
      `pushDefaultTokenFn` resolve the default token from the DB telegram instance only,
      remove `Deps.TelegramBot`. Update `supervisor_test` + `cmd/devflow`.
      (`Deps.TelegramBot` + `cmd/devflow` are removed together with the activation
      deep-link rewrite in p1-3 — they're entangled with the `h.d.TelegramBot` fallback.)
- [ ] p1-3 Activation/bind deep link: resolve username only from the channel instance;
      require `channel_id` + a stored username, else 400 (no env fallback). Update
      `handlers_activation` + handler tests (no-id/no-username → 400, not env default).
- [ ] p1-4 Docs/env: drop `OUROPASS_TELEGRAM_BOT`/`_TOKEN` from `.env.example` and
      `docs/deployment.md` (incl. the stale `OURO_TELEGRAM_*` line); document admin-only
      Telegram config + the breaking migration note.
- [ ] p1-5 Validation: `make test` + `pnpm test` + `shellcheck deploy/install.sh`.

## 4. Test and Acceptance Criteria

- TC-1 No telegram env: config has no `TelegramBot`/`TelegramToken`; a fresh `Load()`
  with a legacy `OUROPASS_TELEGRAM_TOKEN`/`_BOT` set succeeds and ignores them (optional
  deprecation log).
- TC-2 No env-default instance: with no DB telegram instance, the supervisor starts no
  telegram worker; adding one active DB instance runs exactly that instance.
- TC-3 Deep link per-channel only: activation with a `channel_id` whose instance has a
  `bot_username` → deep link uses it; missing `channel_id` or missing username → 400
  (no env-default fallback, no misleading link).
- TC-4 Push default routing: an unscoped push job resolves the default token from the DB
  telegram instance (env token gone); with no instance, no sender (no panic).
- TC-5 Docs/env clean: `.env.example` and `docs/deployment.md` have no telegram env vars
  and carry the admin-only + migration note.
- TC-6 Regression: `make test` + `pnpm test` green; `shellcheck deploy/install.sh` clean.

Pass/fail: TC-1..TC-6 pass; admin (DB) Telegram path behavior unchanged.

## 5. Execution Log (append-only)

- 2026-06-30T20:52:15+08:00 spec drafted (S0017) after S0016 closed: operator decided to
  fully retire the env-default Telegram instance and both telegram env vars, unifying all
  bots under admin configuration. Awaiting promotion to active.
- 2026-06-30T20:58:08+08:00 promoted to active (Start Time set; file moved to docs/specs/).
  Execution order: p1-2/p1-3 (stop referencing cfg.TelegramBot/Token + Deps.TelegramBot)
  run before p1-1 (remove the config fields) so every commit stays buildable.
- 2026-06-30T21:06:00+08:00 p1-2: removed the synthetic env-default instance (envInstanceID
  const, envInstance construction); NewSupervisor drops the envInstance param and desired()'s
  len==0 fallback; instanceToken simplified to decrypt the instance's own stored token (no env
  branch, cfg param dropped); pushDefaultTokenFn resolves the default token from the DB telegram
  instance only. supervisor_test: replaced TestSupervisor_EnvFallback with
  TestSupervisor_NoInstancesNoWorkers. Deps.TelegramBot + cmd/devflow deferred to p1-3 (entangled
  with the activation fallback). go build ./... + go vet ./... clean; worker + issuer tests green.

## 6. Validation Evidence (append-only)
- TC-2 | stack: go | command: go test ./internal/worker/telegram/ | result: pass | note: no DB instance → no worker (TestSupervisor_NoInstancesNoWorkers); adding one runs exactly it; no env fallback

## 7. Change Requests (append-only)
