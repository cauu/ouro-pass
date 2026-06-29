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
- [x] p2-2 On `caddy`-mode `docker compose up -d` failure, print an external-mode hint
      (Docker's bind failure is the detector; no pre-flight port scan).
- [x] p2-3 External mode: generate idempotent `docker-compose.override.yml`
      (issuer host port + caddy profiled-off) and skip ACME/Caddyfile logic.
- [x] p2-4 External mode: generate idempotent `deploy/ouro-pass.nginx.conf` from
      `DOMAIN` + port/bind.
- [x] p2-5 External mode: post-start local `/healthz` self-check + operator-steps final
      message (no false "public ready" claim).
- [x] p3-1 Docs: document external-proxy mode + installer-scope boundary in
      `README.md` and `docs/deployment.md`; fold the runbook's nginx variant in.
- [x] p3-2 Full validation pass against all TCs.
- [x] p4-1 Interrupted-install recovery (completion marker + perceive/fix UX). Write
      `.ouro-configured` only after config fully succeeds; on re-run distinguish a finished
      install (marker present, or legacy real DOMAIN → self-heal the marker) from an
      interrupted one (no marker + placeholder/empty DOMAIN) → notify and offer to
      re-configure (interactive) or fail fast (non-interactive). Add `--reconfigure` /
      `OURO_RECONFIGURE`. Re-configure only rewrites `.env` config fields; never touches
      `./data` (no destructive reset in this item).
- [x] p4-3 Make the generated nginx config first-cert-friendly. On-server acceptance hit
      a real failure: the generated `ouro-pass.nginx.conf` shipped a full 443 block whose
      `ssl_certificate` paths don't exist before issuance, so `nginx -t` failed and
      `certbot --nginx` couldn't run. Fix = emit an **HTTP-only** proxy block (passes
      `nginx -t` with no cert; certbot upgrades it to HTTPS automatically) + correct the
      operator-steps order/wording in install.sh and docs (cp → reload → certbot), plus a
      prerequisites note (certbot installed; 80/443 open).
- [x] p2-6 Interactive proxy-mode prompt + best-effort port detection for its default.
      A fresh interactive install asks "caddy | external" (defaulting via a 80/443
      listener probe); explicit `--proxy`/`OURO_PROXY_MODE` wins; detection is
      interactive-only and silent-fail (no tool / blocked → caddy); non-interactive
      never detects. (Closes the gap where p2-1 added only env/flag parsing, no prompt.)
- [x] p2-7 Re-run mode inference: when config collection is skipped (existing .env) and
      no explicit mode is given, infer `external` from a present
      `docker-compose.override.yml` so re-run messaging/self-check match the deployed
      topology (state was already idempotent; this fixes installer output only).

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

- TC-12 Interactive prompt: a fresh interactive run prompts for proxy mode; default is
  `external` when a listener is detected on 80/443, else `caddy`; an explicit
  `--proxy`/`OURO_PROXY_MODE` overrides the detected default; non-interactive neither
  prompts nor detects (uses the explicit value or `caddy`).
- TC-13 Detection is best-effort/silent: with no `ss`/`netstat` available (or the probe
  erroring), the run proceeds with the `caddy` default and prints no error.
- TC-14 Re-run inference: with an existing `.env` + `docker-compose.override.yml` and no
  explicit mode, the installer treats the deployment as `external` (external
  messaging/self-check), not `caddy`; an explicit `--proxy` still overrides.
- TC-15 Interrupted-install detection: `.env` present with the placeholder/empty DOMAIN and
  no `.ouro-configured` → interactive run reports an unfinished install and offers
  re-configure; non-interactive fails fast unless `--reconfigure`/`OURO_RECONFIGURE`.
- TC-16 Finished install untouched: `.ouro-configured` present → re-run keeps config (no
  re-prompt); a legacy finished install (no marker but real DOMAIN) is treated as
  configured and self-heals the marker.
- TC-17 Marker lifecycle: a completed configure writes `.ouro-configured` as the final
  step; an abort before that step leaves no marker (so the next run detects interrupted).
- TC-18 Generated nginx config is first-cert-friendly: `ouro-pass.nginx.conf` is HTTP-only
  (no `443`/`ssl_certificate`), proxies to `${bind}:${port}` with the four forwarded
  headers, and passes `nginx -t` with no certificate present; the operator steps read
  cp → reload → `certbot --nginx`.

Pass/fail: all TC-1..TC-18 pass; `caddy`-mode regression (TC-1) is mandatory.

## 5. Execution Log (append-only)

- 2026-06-29T14:15:56+08:00 spec drafted (S0013), awaiting review before promotion to active.
- 2026-06-29T14:15:56+08:00 design refined: dropped pre-flight port scan in favor of Docker bind-failure + hint (per review). Promoted draft → active.
- 2026-06-29T14:15:56+08:00 p1-1 started: remove Telegram/channel config from installer.
- 2026-06-29T14:15:56+08:00 p1-1 completed: dropped TELEGRAM_BOT/TOKEN prompts + set_env writes + non-interactive env list in deploy/install.sh; added /admin channels pointer to --help and final message; annotated OUROPASS_TELEGRAM_* as optional/legacy in .env.example.
- 2026-06-29T14:15:56+08:00 p1-1 spec annotations (CR + TC-11) and p2-1 deferred to ride with p2-1 commit.
- 2026-06-29T14:15:56+08:00 p2-1 started/completed: added OURO_PROXY_MODE (default caddy) + OURO_HTTP_PORT (8080) + OURO_BIND_ADDR (127.0.0.1) settings, `--proxy MODE` flag, --help text, and early mode validation. New vars are not yet consumed → caddy flow byte-for-byte unchanged. Noted: `--proxy` missing-value exit-code quirk is identical to existing --ref/--dir (pre-existing, out of scope).
- 2026-06-29T14:15:56+08:00 p2-2 started/completed: wrapped the start `docker compose up -d` in an if/else (suspends set -e) so a failure prints an enriched hint — partial-state/idempotent re-run guidance + `--proxy external` alternative, gated on caddy mode. Verified via isolated harness (caddy-fail→hint+exit1, external-fail→no caddy hint+exit1, caddy-success→Done+exit0).
- 2026-06-29T14:15:56+08:00 p2-3 started/completed: external mode now (a) skips the ACME_EMAIL prompt (sets ACME_EMAIL="" to stay set -u safe), (b) skips the Caddyfile email-block injection, and (c) writes an idempotent docker-compose.override.yml publishing issuer on ${OURO_BIND_ADDR}:${OURO_HTTP_PORT} and assigning caddy an inactive profile. Risk retired: verified on real docker compose v2.31 that the override-applied profile excludes caddy from default `up` (TC-3).
- 2026-06-29T14:15:56+08:00 p2-4 started/completed: external mode writes an idempotent deploy/ouro-pass.nginx.conf reference (HTTP→HTTPS redirect + 443 proxy block) with DOMAIN/bind/port substituted and nginx $vars kept literal; includes a comment on the two TLS paths + the cert-ordering caveat (nginx -t fails before certbot). Verified all TC-5 elements via isolated generation.
- 2026-06-29T14:15:56+08:00 p2-5 started/completed: external success branch now probes http://bind:port/healthz with a bounded retry (15×2s, since up -d returns pre-health) and prints an honest contract — "not yet reachable over HTTPS" + the 3 operator steps (cp config → certbot → reload) + verify/admin/channels pointers; caddy branch unchanged. Verified mode split (external-healthy / external-unhealthy / caddy) via isolated harness.
- 2026-06-29T14:15:56+08:00 p3-1 started/completed: added "Behind an existing reverse proxy" section to docs/deployment.md, a pointer + anchor in README.md, and gitignore entries for the two installer-generated artifacts (docker-compose.override.yml, deploy/ouro-pass.nginx.conf). Canonical external-proxy guidance now lives in deployment.md; docs/server-test-runbook.md left as an untracked personal doc.
- 2026-06-29T14:20:00+08:00 Change Request accepted (post p3-2): (1) the interactive proxy-mode prompt was specified in the design but never implemented (p2-1 added only env/flag parsing) — a defect against the spec's own design; (2) operator asked to also run a best-effort 80/443 probe to pre-select that prompt's default, silent-fail on permission/tool gaps. Added items p2-6 (prompt + detection) and p2-7 (re-run inference) + TC-12/13/14. This reopens local-validation work; spec stays active.
- 2026-06-29T14:20:00+08:00 p2-6 started/completed: OURO_PROXY_MODE now resolves as explicit(flag/env) > interactive detect > caddy; added detect_proxy_default() (ss→netstat, address-column $4, regex [:.](80|443)$ so :8080 never matches) used interactive-only; added the "Reverse proxy: caddy|external" ask before the (caddy-only) ACME prompt with post-prompt re-validation; re-run branch keeps a caddy fallback (superseded by p2-7). Verified: shellcheck clean; detection busy80/busy443→external, only8080/notool→caddy; precedence explicit-wins / NI-no-detect / interactive-detect all correct.
- 2026-06-29T19:55:00+08:00 p4-3 started/completed: changed the generated ouro-pass.nginx.conf from (HTTP-redirect + 443-with-cert-paths) to a single HTTP-only proxy block — so it passes `nginx -t` before any cert exists and `certbot --nginx` upgrades it to HTTPS (copying the proxy + forwarded headers into the 443 server it creates). Reordered/reworded the installer's operator steps to cp → reload → certbot, added a prereqs line (certbot+plugin installed; 80/443 open in host firewall AND cloud SG), and updated docs/deployment.md to match. Verified: shellcheck clean; generated config has no 443/ssl_certificate; real `nginx -t` (nginx:alpine) passes with NO cert present; all four forwarded headers present.
- 2026-06-29T14:35:00+08:00 p4-1 started/completed: added a `.ouro-configured` completion marker written as the final config step; replaced the binary ENV_PREEXISTED gate with a three-state decision (fresh / --reconfigure / finished-keep / interrupted-recover) via is_configured() (marker present, or legacy real-DOMAIN bridge that self-heals the marker); interrupted installs now warn + offer re-configure interactively, or fail fast non-interactively (pointing at --reconfigure / delete .env). Added `--reconfigure`/`OURO_RECONFIGURE`, --help text, gitignore + a deployment.md troubleshooting entry. Re-configure only rewrites .env config fields; ./data untouched. Verified: shellcheck clean (fixed an SC2154 on the eval-assigned _redo); all 7 decision branches correct (fresh→configure, marker→keep, legacy→keep+heal, --reconfigure→configure, interrupted+NI→fail, interrupted+yes→configure, interrupted+no→abort).
- 2026-06-29T14:20:00+08:00 p2-7 started/completed: re-run branch (existing .env) now infers OURO_PROXY_MODE=external when docker-compose.override.yml is present (else caddy), with an explicit flag/env still overriding — so re-run messaging/self-check match the deployed topology. Verified via harness: no-override→caddy, override→external, override+explicit-caddy→caddy. shellcheck clean. Fully passed locally: TC-2, TC-3, TC-5, TC-6, TC-8, TC-10 (real shellcheck via docker, clean), TC-1 (artifact-absence in caddy mode), TC-4 (probe logic), TC-9 (static: update.sh only calls compose, never touches the override → caddy stays excluded by construction). Confirmed update.sh has no override/Caddyfile/refetch coupling. Deferred to an operator Docker host with the production image + a real domain (environment-blocked here): TC-4 real /healthz over the stack, TC-7 full real 80/443 conflict hint, TC-11 idempotent re-run after a real conflict. p3-2 closes the local-validatable surface; spec remains active pending optional on-server confirmation + user sign-off.

## 6. Validation Evidence (append-only)

- TC-2 | stack: other | command: grep -ni 'OUROPASS_TELEGRAM\|OURO_TELEGRAM\|TELEGRAM_BOT\|TELEGRAM_TOKEN' deploy/install.sh | result: pass | note: no Telegram prompts/env-writes remain; only two intentional /admin pointer strings (--help + final message).
- TC-10 | stack: other | command: sh -n deploy/install.sh | result: pass | note: shellcheck not installed on host; syntax check via `sh -n` clean. Full shellcheck deferred to p3-2 validation pass.
- TC-1 (partial) | stack: other | command: sh -n deploy/install.sh + manual read | result: pass | note: p2-1 vars added but unconsumed; no override produced, caddy path unchanged. Full caddy dry-run regression re-verified at p3-2.
- TC-7 (partial) | stack: other | command: sh deploy/install.sh --help; sh deploy/install.sh --proxy bogus | result: pass | note: --help lists --proxy + reverse-proxy env vars; invalid mode fails fast (exit 1) before preflight; default mode = caddy.
- TC-8 (partial, caddy-fail path) | stack: other | command: isolated start-block harness with stubbed failing `docker` | result: pass | note: caddy-mode up failure prints partial-state + idempotent-rerun + --proxy external hint and exits non-zero; external mode skips the caddy hint; success path prints Done. Real-stack caddy port-conflict (TC-7 full) + idempotent re-run (TC-11) to be exercised at p3-2 on a Docker host.
- TC-3 | stack: other | command: docker compose config --services (real docker-compose.yml + generated override + minimal .env), v2.31.0 | result: pass | note: default services = {issuer, postgres}, caddy ABSENT; `--profile caddy-disabled` re-includes caddy (proves profiled-off not removed); issuer publishes 127.0.0.1:8080:8080.
- TC-5 | stack: other | command: isolated heredoc generation + grep assertions | result: pass | note: server_name=pass.example.com, proxy_pass=http://127.0.0.1:8080, all four forwarded headers present incl X-Forwarded-Proto; nginx $host/$scheme/etc kept literal; DOMAIN/bind/port expanded.
- TC-4 (logic) | stack: other | command: isolated success-branch harness (stub docker/curl/sleep) | result: pass | note: external mode probes /healthz with bounded retry; healthy→"issuer healthy", unhealthy→warn; real /healthz on a Docker host deferred to p3-2.
- TC-8 | stack: other | command: same harness, assert messaging by mode | result: pass | note: external prints "not yet reachable over HTTPS" + 3 operator steps and NEVER the "Done. Open https://…" line; caddy prints only the Done line. Honest-state contract met.
- TC-10 (full) | stack: other | command: docker run --rm -v $PWD:/mnt -w /mnt koalaman/shellcheck:stable deploy/install.sh | result: pass | note: real shellcheck, exit 0, zero warnings.
- TC-1 (artifacts) | stack: other | command: isolated guarded-block test, mode=caddy | result: pass | note: caddy mode generates neither docker-compose.override.yml nor deploy/ouro-pass.nginx.conf; new vars unconsumed in caddy path. Full caddy dry-run (network+image) left for on-server.
- TC-6 | stack: other | command: isolated guarded-block test, external re-run with seeded sentinel files | result: pass | note: second run warns and keeps existing override + nginx.conf (no clobber), mirroring .env handling.
- TC-9 (static) | stack: other | command: grep -nE 'docker compose|override|fetch|Caddyfile' deploy/update.sh | result: pass | note: update.sh only runs docker compose pull/up -d/ps/exec/logs; never references or deletes the override, never refetches files. With the override auto-loaded, caddy stays excluded (per TC-3) → update path compatible by construction. Full live update recommended on-server.
- TC-4 (real) / TC-7 (full) / TC-11 | stack: other | command: (deferred — requires production issuer image + real domain on a Docker host) | result: deferred | note: environment-blocked here; logic/static evidence captured under TC-4-logic, TC-8, TC-3, TC-9. To be exercised by the operator on the target server.
- TC-12 | stack: other | command: isolated harness — detect_proxy_default with realistic `ss -ltnH` rows + resolution precedence | result: pass | note: busy :80/:443 → external default, only :8080 → caddy (8080 not matched); explicit --proxy/env overrides detection; non-interactive neither prompts nor detects.
- TC-13 | stack: other | command: harness with command -v stubbed to fail (no ss/netstat) | result: pass | note: probe yields caddy, no error raised (silent-fail).
- TC-14 | stack: other | command: isolated re-run-inference harness toggling override presence + explicit mode | result: pass | note: no override→caddy, override present→external, override+explicit caddy→caddy (explicit overrides). shellcheck clean on the full script.
- TC-15 | stack: other | command: decision-block harness, .env DOMAIN=pass.example.com + no marker | result: pass | note: interactive → warns "Unfinished install" + asks re-configure (yes→DO_CONFIGURE=1, no→abort); non-interactive → fail-fast err pointing at --reconfigure / delete .env.
- TC-16 | stack: other | command: decision-block harness, marker present and legacy (no marker, real DOMAIN) | result: pass | note: marker present → DO_CONFIGURE=0 (keep); legacy real DOMAIN → DO_CONFIGURE=0 and marker self-healed to present; --reconfigure forces DO_CONFIGURE=1 even with marker.
- TC-17 | stack: other | command: grep placement + harness | result: pass | note: `: > "$CONFIGURED_MARKER"` is the last statement of the configure branch (after the Caddyfile step, before the branch `fi`); fresh install leaves marker absent until that step runs, so an earlier abort is detected next run. shellcheck clean.
- TC-18 | stack: other | command: regenerate config + `grep -E '443|ssl_certificate'` + `docker run --rm -v conf:/etc/nginx/conf.d/ouro.conf nginx:alpine nginx -t` | result: pass | note: generated config is HTTP-only (no 443/ssl_certificate); real nginx -t reports "syntax is ok / test is successful" with NO cert present (old 443-with-cert config would fail here); four forwarded headers present; installer steps reordered to cp → reload → certbot.
- TC-4 (real, on-server) | stack: other | command: curl http://<domain>/healthz through public :80 → nginx → issuer | result: pass | note: operator-confirmed deploy success; HEAD→405 Allow: GET (chain intact), GET→200; HTTPS reached after certbot. External reverse-proxy mode validated end-to-end on the target host.

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

- 2026-06-29T14:35:00+08:00 Change Request accepted (during on-server testing): an
  interrupted install (operator quit right after the DOMAIN prompt) left a placeholder
  `.env` (init.sh creates `.env` BEFORE the questions), so the next run took the "existing
  install" path, skipped all prompts, and deployed with the example DOMAIN — i.e. the
  installer was not safely re-runnable after an abort. This is a pre-existing S0011
  robustness gap, but it blocks S0013's own on-server acceptance, and the one-active-spec
  rule precludes a parallel S0014 — so it is appended here as bucket p4 rather than a new
  spec. Chosen approach: A (completion marker) + perceive/fix UX, with a legacy real-DOMAIN
  bridge for installs predating the marker. Added p4-1 + TC-15/16/17. Out of scope for this
  item: destructive `--reset` and DOMAIN input sanitization (candidate follow-ups).

- 2026-06-29T19:50:00+08:00 On-server acceptance (operator host, mainnet/koios, nginx
  already on 80/443): external mode deployed successfully end-to-end. ENVIRONMENT issues
  surfaced and resolved (not code): user not in `docker` group (added), `certbot` not
  installed (installed with nginx plugin), ufw blocking :80 (allowed 80/443). p4-1 worked
  as designed — an interrupted run was detected on re-run and recovered. Public reachability
  confirmed: `curl http://<domain>/healthz` reached the issuer through nginx (HEAD→405
  Allow: GET = chain intact; GET→200). One real DEFECT found → p4-3: the generated nginx
  config pre-configured TLS (443 block with non-existent cert paths), breaking `nginx -t`
  and `certbot --nginx` on first issuance.
