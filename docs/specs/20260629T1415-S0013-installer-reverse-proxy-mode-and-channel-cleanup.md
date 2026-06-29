# Installer reverse-proxy mode + channel-config cleanup

Spec-ID: S0013
Status: active
Created Time: 2026-06-29T14:15:56+08:00
Start Time: 2026-06-29T14:15:56+08:00
Completion Time:
Previous Spec-ID:
Closure Reason:

## 1. Requirement Details

### Background

`deploy/install.sh` (S0011) bootstraps a single-host deployment and always brings up
the bundled **Caddy** container to terminate TLS on ports 80/443. On hosts that already
run a reverse proxy (e.g. nginx occupying 80/443), `docker compose up -d` fails on the
Caddy container because the ports are taken.

Separately, the installer currently prompts for and writes `OUROPASS_TELEGRAM_BOT` /
`OUROPASS_TELEGRAM_TOKEN`. Since S0005, channels are first-class **DB instances**
managed in `/admin` (per-instance encrypted tokens, hot-reloaded by the telegram
supervisor); the env vars survive only as a legacy implicit "default" instance. Putting
channel credentials in the installer both (a) bloats it linearly as channel types grow
and (b) fights the architecture — DB/admin entities do not belong in pre-boot env config.

### Scope

Two changes, both confined to `deploy/install.sh` (+ generated artifacts and docs):

1. **External reverse-proxy mode** — an explicit `OURO_PROXY_MODE` toggle
   (`caddy` default | `external`). In `external` mode the installer does not run Caddy,
   publishes the issuer on a local host port, and emits a ready-to-use nginx server block
   for the operator to install themselves.
2. **Remove channel config from the installer** — drop the Telegram prompts, `set_env`
   writes, and non-interactive env vars; point operators to `/admin` for channels.

### Constraints

- `caddy` mode = exactly the current behavior; fully backward compatible.
- Do not edit the committed `docker-compose.yml`, `deploy/Caddyfile`, or `deploy/init.sh`.
  Compose-level changes for `external` mode go through a generated
  `docker-compose.override.yml` only.
- `update.sh` must keep working unchanged in both modes.
- No fragile pre-flight port scan: `OURO_PROXY_MODE` is the single source of truth.
  Docker's own bind attempt is the reliable conflict detector — on a `caddy`-mode start
  failure the installer prints a hint suggesting `external` mode. Behavior never changes
  silently.
- The installer must not modify host nginx config or run certbot; it only generates a
  config snippet and prints next steps. TLS provisioning stays with the operator.
- Idempotent / safe to re-run: generated `docker-compose.override.yml` and the nginx
  snippet are written only when absent; if present, keep and warn (mirrors `.env` handling).
- Keep `OUROPASS_TELEGRAM_*` lines in `.env.example` as optional/legacy (do not delete),
  preserving the back-compat default-instance path.

### Non-goals

- No automatic TLS / certbot / host-nginx management.
- No support for a reverse proxy on a *different* host beyond exposing a bindable port
  (`OURO_BIND_ADDR`); cross-host networking/firewalling is the operator's concern.
- No changes to the issuer binary, compose service definitions in `docker-compose.yml`,
  or the channel/admin runtime.
- No Apache/Traefik/etc. snippet generation in this spec (nginx only; neutral template
  may be future work).

## 2. Outline Design

### Modules impacted

- `deploy/install.sh` — flag/env parsing, mode branch, override + snippet generation,
  local health check, final-message branch, removal of channel prompts.
- `deploy/ouro-pass.nginx.conf` — **new generated artifact** (server block with `DOMAIN`
  and port substituted). Not committed; produced at install time.
- `docker-compose.override.yml` — **new generated artifact** (external mode only).
- `.env.example` — annotate `OUROPASS_TELEGRAM_*` as optional/legacy (append-only note).
- `README.md` / `docs/deployment.md` — document the external-proxy mode and the
  installer-scope boundary (channels via `/admin`).

### New configuration knobs (env / flags)

| Var / flag | Meaning | Default |
|---|---|---|
| `OURO_PROXY_MODE` / `--proxy <mode>` | `caddy` (bundled TLS) \| `external` (existing proxy) | `caddy` |
| `OURO_HTTP_PORT` | host port the issuer publishes in external mode | `8080` |
| `OURO_BIND_ADDR` | bind address for the published port | `127.0.0.1` |

Removed knobs: `OURO_TELEGRAM_BOT`, `OURO_TELEGRAM_TOKEN` (and their prompts / `set_env`).

### External-mode behavior (A–E)

- **A. Front half** — preflight, download, `init.sh`, secrets, `./data`, `DOMAIN`,
  network/chain/tag/owner prompts: unchanged. `ACME_EMAIL` prompt + Caddyfile email-block
  logic: skipped. `OURO_HTTP_PORT`/`OURO_BIND_ADDR` default silently, env-overridable.
- **B. Generated artifacts** —
  `docker-compose.override.yml`:
  ```yaml
  services:
    issuer:
      ports: ["127.0.0.1:8080:8080"]
    caddy:
      profiles: ["caddy-disabled"]   # profiled service is excluded from default `up`
  ```
  `deploy/ouro-pass.nginx.conf`: server block (80→443 redirect; TLS; `proxy_pass` to the
  published port; sets `Host`, `X-Real-IP`, `X-Forwarded-For`, `X-Forwarded-Proto`).
- **C. Start + self-check** — `docker compose up -d` (override auto-loaded; caddy excluded
  by profile → only postgres + issuer). Then `curl -fsS http://<bind>:<port>/healthz`.
- **D. Final message (contract)** — must NOT claim public readiness. Print the 3 operator
  steps: place snippet → `certbot --nginx -d <domain>` → `nginx -t && reload`, then verify
  `https://<domain>/healthz`.
- **E. update.sh** — unchanged; override auto-loaded and caddy stays profiled across pulls.

### Conflict handling (no pre-scan)

We deliberately do NOT scan ports up front (`ss`/`lsof` are non-portable, may need root,
and mishandle IPv4/IPv6 + container-held ports). The authoritative detector is Docker
itself: in `caddy` mode it tries to bind 80/443 and fails loudly if they are taken.

- The installer catches a non-zero `docker compose up -d` exit in `caddy` mode and prints
  a hint: "ports 80/443 may be in use — re-run with `OURO_PROXY_MODE=external`".
- The interactive prompt still defaults to `caddy`; an operator who knows they already run
  nginx picks `external` explicitly.
- Behavior is always an explicit function of `OURO_PROXY_MODE`; nothing auto-switches.

### Channel-config removal

- Delete the `TELEGRAM_BOT` / `TELEGRAM_TOKEN` `ask` calls + `set_env` writes.
- Remove `OURO_TELEGRAM_*` from the non-interactive env list and `--help`.
- `.env.example`: append a note marking `OUROPASS_TELEGRAM_*` optional/legacy; preferred
  path is creating channel instances in `/admin`.
- Both `caddy` and `external` final messages mention: add channels in `/admin` after deploy.

### Risk and rollback

- Risk: assigning `profiles:` to `caddy` via an override file might not exclude it from
  default `up` on older compose. Mitigation: verify on the target compose v2; if it does
  not exclude, fall back to override setting `caddy.deploy.replicas: 0` or documenting a
  `--profile`-gated start. Covered by TC-3.
- Risk: published port reachable beyond host. Mitigation: default bind `127.0.0.1`.
- Rollback: changes are additive to a shell script + generated files; revert the commit.
  No released schema or runtime impact.

## References

- `deploy/install.sh` — current installer (S0011).
- `deploy/update.sh` — must remain compatible (S0012).
- `docker-compose.yml` — issuer/postgres/caddy service definitions; issuer already sets
  `OUROPASS_TLS=true` / `OUROPASS_TRUSTED_PROXY=true`.
- `deploy/Caddyfile` — TLS-terminating proxy replaced by nginx in external mode.
- `docs/multi-channel-instances.md` (S0005) — channels are DB instances managed in `/admin`.
- `server/internal/config/config.go` — `OUROPASS_TELEGRAM_*`, `TRUSTED_PROXY`, `TLS` knobs.
- `server/internal/httpapi/router.go` — `/healthz`, `/.well-known/ouropass/jwks.json` paths.
- Memory: `installer-scope-boundary` — installer = pre-boot env only, never DB/admin entities.

## 3. Execution Plan

- [x] p1-1 Remove Telegram/channel config from installer (prompts, `set_env`,
      non-interactive env vars, `--help`); add `/admin` pointer to final messages;
      annotate `OUROPASS_TELEGRAM_*` as optional/legacy in `.env.example`.
- [x] p2-1 Add `OURO_PROXY_MODE` + `OURO_HTTP_PORT` + `OURO_BIND_ADDR` parsing
      (`--proxy` flag, env, `--help`), defaulting to `caddy`; no behavior change in
      `caddy` mode.
- [ ] p2-2 On `caddy`-mode `docker compose up -d` failure, print an external-mode hint
      (Docker's bind failure is the detector; no pre-flight port scan).
- [ ] p2-3 External mode: generate idempotent `docker-compose.override.yml`
      (issuer host port + caddy profiled-off) and skip ACME/Caddyfile logic.
- [ ] p2-4 External mode: generate idempotent `deploy/ouro-pass.nginx.conf` from
      `DOMAIN` + port/bind.
- [ ] p2-5 External mode: post-start local `/healthz` self-check + operator-steps final
      message (no false "public ready" claim).
- [ ] p3-1 Docs: document external-proxy mode + installer-scope boundary in
      `README.md` and `docs/deployment.md`; fold the runbook's nginx variant in.
- [ ] p3-2 Full validation pass against all TCs.

## 4. Test and Acceptance Criteria

- TC-1 `caddy` mode unchanged: a dry run (or `OURO_START=no`) produces no override file,
  still wires Caddy, and writes the same `.env` keys as before minus Telegram.
- TC-2 No Telegram prompts/keys: interactive and `--non-interactive` runs never ask for
  or write `OUROPASS_TELEGRAM_*`; `--help` lists no `OURO_TELEGRAM_*`.
- TC-3 External mode excludes Caddy: with a generated override, `docker compose config
  --services` (or `up -d` + `ps`) shows caddy NOT started; issuer + postgres run; issuer
  publishes `<bind>:<port>`.
- TC-4 External self-check: `curl -fsS http://127.0.0.1:<port>/healthz` returns
  `{"status":"ok"}` after start.
- TC-5 Generated nginx snippet: `deploy/ouro-pass.nginx.conf` contains the resolved
  `server_name <domain>`, `proxy_pass http://<bind>:<port>`, and the four forwarded
  headers incl. `X-Forwarded-Proto`.
- TC-6 Idempotency: re-running external install keeps an existing override + snippet
  (warns, no clobber), mirroring `.env` handling.
- TC-7 Non-interactive: `external` mode runs without prompts given the required `OURO_*`
  vars; a `caddy`-mode start failure prints the external-mode hint (simulated by a busy
  port or a forced non-zero `up`).
- TC-8 Final-message contract: external mode prints the 3 operator steps and does NOT
  print a "Done, open https://…" success line.
- TC-9 `update.sh` compatibility: `docker compose pull && up -d` under an external-mode
  override still excludes caddy and keeps issuer/postgres healthy.
- TC-10 `shellcheck deploy/install.sh` passes (no new warnings vs. baseline).
- TC-11 Idempotent re-run after a start conflict: when `docker compose up -d` fails on a
  port bind (caddy) leaving postgres+issuer running, re-running `up -d` after the port is
  freed converges to the full stack with no duplicate containers and no data loss
  (`docker compose ps` shows all healthy; `./data/postgres` intact).

Pass/fail: all TC-1..TC-11 pass; `caddy`-mode regression (TC-1) is mandatory.

## 5. Execution Log (append-only)

- 2026-06-29T14:15:56+08:00 spec drafted (S0013), awaiting review before promotion to active.
- 2026-06-29T14:15:56+08:00 design refined: dropped pre-flight port scan in favor of Docker bind-failure + hint (per review). Promoted draft → active.
- 2026-06-29T14:15:56+08:00 p1-1 started: remove Telegram/channel config from installer.
- 2026-06-29T14:15:56+08:00 p1-1 completed: dropped TELEGRAM_BOT/TOKEN prompts + set_env writes + non-interactive env list in deploy/install.sh; added /admin channels pointer to --help and final message; annotated OUROPASS_TELEGRAM_* as optional/legacy in .env.example.
- 2026-06-29T14:15:56+08:00 p1-1 spec annotations (CR + TC-11) and p2-1 deferred to ride with p2-1 commit.
- 2026-06-29T14:15:56+08:00 p2-1 started/completed: added OURO_PROXY_MODE (default caddy) + OURO_HTTP_PORT (8080) + OURO_BIND_ADDR (127.0.0.1) settings, `--proxy MODE` flag, --help text, and early mode validation. New vars are not yet consumed → caddy flow byte-for-byte unchanged. Noted: `--proxy` missing-value exit-code quirk is identical to existing --ref/--dir (pre-existing, out of scope).

## 6. Validation Evidence (append-only)

- TC-2 | stack: other | command: grep -ni 'OUROPASS_TELEGRAM\|OURO_TELEGRAM\|TELEGRAM_BOT\|TELEGRAM_TOKEN' deploy/install.sh | result: pass | note: no Telegram prompts/env-writes remain; only two intentional /admin pointer strings (--help + final message).
- TC-10 | stack: other | command: sh -n deploy/install.sh | result: pass | note: shellcheck not installed on host; syntax check via `sh -n` clean. Full shellcheck deferred to p3-2 validation pass.
- TC-1 (partial) | stack: other | command: sh -n deploy/install.sh + manual read | result: pass | note: p2-1 vars added but unconsumed; no override produced, caddy path unchanged. Full caddy dry-run regression re-verified at p3-2.
- TC-7 (partial) | stack: other | command: sh deploy/install.sh --help; sh deploy/install.sh --proxy bogus | result: pass | note: --help lists --proxy + reverse-proxy env vars; invalid mode fails fast (exit 1) before preflight; default mode = caddy.

## 7. Change Requests (append-only)

- 2026-06-29T14:15:56+08:00 Failure-semantics clarification (informs p2-2 + p2-5).
  `docker compose up -d` is not transactional: a caddy port-bind failure leaves
  postgres+issuer running (partial bring-up), there is no auto-rollback, but re-running
  `up -d` is idempotent and converges once the port is freed; `./data` persists and is not
  corrupted by the partial failure. Therefore:
  - p2-2's hint must say more than "ports in use": note that postgres/issuer may already be
    running (normal partial state), that re-running `docker compose up -d` after freeing the
    port completes the stack, and offer `OURO_PROXY_MODE=external` as the alternative.
  - The same honest-state principle applies to p2-5's final message (no false "nothing
    deployed" / no false "all done").
  - Added TC-11 to cover idempotent re-run after a conflict.
