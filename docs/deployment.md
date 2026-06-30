# Ouro Pass — Deployment Guide

One-command self-hosting with Docker Compose. After you set a few environment
variables, `docker compose up -d` brings up the whole stack.

## Architecture

The Ouro Pass **issuer is a single Go binary** that serves everything:

- the HTTP API,
- the **Admin SPA** (baked into the binary, served at `/admin`),
- the background workers (reconciliation / push / Telegram).

So there is no separate frontend container. The compose stack is three services:

```
                 ┌─────────────────────────── your host ───────────────────────────┐
   Internet ──▶  caddy (TLS, :80/:443)  ──▶  issuer (:8080, API + /admin + workers)
                                                     │
                                                     ▼
                                              postgres (:5432)
   persistent state (host bind-mounts):  ./data/postgres   ./data/caddy
```

- **caddy** terminates TLS and gets/renews a Let's Encrypt certificate for your
  domain automatically.
- **issuer** is stateless (signing keys live in the DB); pulled as a prebuilt
  multi-arch image from GHCR (`ghcr.io/cauu/ouro-pass`).
- **postgres** holds all data; bind-mounted to `./data/postgres` so it is visible
  on the host and easy to back up.

## Prerequisites

- Docker Engine 24+ and the Compose v2 plugin (`docker compose version`).
- `openssl` (for `deploy/init.sh`).
- A **public domain** (e.g. `pass.example.com`) with an A/AAAA record pointing at
  this host, and inbound TCP **80 and 443** open (Caddy needs both for ACME).
- A Cardano wallet whose stake key will be the admin **owner**.

## Quick start

### Install script (recommended)

One command checks prerequisites, downloads the compose stack, generates secrets,
prompts for the essentials (domain, owner wallet) and starts it:

```sh
curl -fsSL https://raw.githubusercontent.com/cauu/ouro-pass/main/deploy/install.sh | sh
```

- **Inspect first:** `curl -fsSLO .../deploy/install.sh && less install.sh && sh install.sh`
- **Pin a release:** `... /v0.2.0/deploy/install.sh | OURO_REF=v0.2.0 sh`
- **Non-interactive (CI):** pipe with `--non-interactive` and `OURO_*` env vars
  (`OURO_DOMAIN`, `OURO_OWNER_ADDR` or `OURO_OWNER_KEYS`, `OUROPASS_TAG`,
  `OURO_START`). Run `install.sh --help` for the full list.

The installer never overwrites existing secrets and is safe to re-run.

### Manual setup

You do **not** need the source tree — only these files (keep the directory layout):

```
ouro-pass/
├── docker-compose.yml
├── .env.example
└── deploy/
    ├── Caddyfile
    ├── init.sh
    └── update.sh
```

Grab them from the repo, e.g.:

```sh
mkdir -p ouro-pass/deploy && cd ouro-pass
BASE=https://raw.githubusercontent.com/cauu/ouro-pass/main
curl -fsSLO $BASE/docker-compose.yml
curl -fsSLO $BASE/.env.example
curl -fsSL  $BASE/deploy/Caddyfile -o deploy/Caddyfile
curl -fsSL  $BASE/deploy/init.sh   -o deploy/init.sh
curl -fsSL  $BASE/deploy/update.sh -o deploy/update.sh
chmod +x deploy/init.sh deploy/update.sh
```

> Setting `ACME_EMAIL` for cert-expiry notices? The installer wires it automatically;
> for a manual setup, add a `{ email you@example.com }` block at the top of
> `deploy/Caddyfile` (an empty email value breaks Caddy).

Then:

```sh
# 1) generate .env with strong random secrets + create ./data dirs
./deploy/init.sh

# 2) edit .env — set at minimum:
#      DOMAIN=pass.example.com
#      OUROPASS_OWNER_KEYS=<your owner stake-key hash>     (see below)
#    review OUROPASS_TAG (pin a version); chain source is Koios-only, network is per-attestor (set in /admin)
$EDITOR .env

# 3) pull + start everything
docker compose up -d

# 4) open https://<DOMAIN>/admin and sign in with your owner wallet
```

`docker compose up -d` pulls the issuer image from GHCR (no local build), then
starts postgres → issuer → caddy in dependency order. The issuer migrates the
database on startup; there is no separate migration step.

> The GHCR package must be **public** for an unauthenticated `docker pull`. The
> maintainer sets this once in the GitHub repo → Packages → package settings.

### Getting your owner key hash

`OUROPASS_OWNER_KEYS` is a comma-separated list of on-chain owner stake-key
hashes allowed to sign in to `/admin` as owner. Compute it straight from the
image — no source checkout needed:

```sh
docker run --rm ghcr.io/cauu/ouro-pass stake-hash stake1...   # prints the hash
```

(From a full checkout you can also use `cd server && make stake-hash ADDR=stake1...`.)

## Configuration reference

All knobs live in `.env` (see `.env.example` for the annotated list). Highlights:

| Variable | Purpose |
|---|---|
| `DOMAIN` | Public domain; Caddy's cert + the token `iss` (`https://${DOMAIN}`). |
| `ACME_EMAIL` | Optional Let's Encrypt contact for expiry notices. |
| `OUROPASS_FIELD_KEY` | **Secret.** AES-256 master key for 🔒 fields (`openssl rand -hex 32`). |
| `OUROPASS_SERVER_SALT` | **Secret.** HMAC salt for the pseudonymous `sub` (`openssl rand -hex 16`). |
| `OUROPASS_CHAIN_API_KEY` | Optional Koios API key (paid tiers); blank = unauthenticated public access. |

> **Chain source is Koios-only (S0015):** the issuer always reads stake from the public
> Koios API with built-in per-network endpoints. There is no chain-source setting — the
> legacy `OUROPASS_CHAIN_KIND`, `OUROPASS_KOIOS_BASE_URL[_<NET>]`, `OUROPASS_NODE_SOCKET`
> and `OUROPASS_CARDANO_CLI` vars are removed and ignored if still present.
>
> **Network is per-attestor (S0014):** it is chosen in the admin UI per pool (defaults to
> `mainnet`), not set globally. `OUROPASS_NETWORK` is deprecated and ignored.
| `OUROPASS_OWNER_KEYS` | Owner stake-key hashes for `/admin`. |
| `POSTGRES_USER` / `_PASSWORD` / `_DB` | Bundled Postgres credentials. |

> **Telegram is configured in admin (S0017):** add bots in `/admin → Channels` (each
> instance stores its own token + username and supplies its activation deep link).
> There is no Telegram env — `OUROPASS_TELEGRAM_BOT`/`_TOKEN` are removed and ignored.
> **Migration:** a deploy that previously set `OUROPASS_TELEGRAM_TOKEN`/`_BOT` must
> re-add that bot as a Channel in `/admin` after upgrading (the env bot no longer runs).

> Derived automatically in `docker-compose.yml` (do not set in `.env`):
> `OUROPASS_ADDR`, `OUROPASS_ISSUER`, `OUROPASS_TRUSTED_PROXY`, `OUROPASS_TLS`,
> `OUROPASS_DB_DRIVER`, `OUROPASS_DB_DSN`.

> ⚠️ Never reuse the fixed dev keys from `server/Makefile`. Rotating
> `OUROPASS_FIELD_KEY` after data exists makes previously-encrypted 🔒 fields
> unreadable.

## Chain data source

The issuer reads on-chain stake facts from **Koios** — a federated, open API — and
this is the single chain origin (S0015). Public per-network endpoints
(`api`/`preprod`/`preview.koios.rest`) are built in and resolved automatically from
each attestor's network; there is nothing to configure. The eligibility path is a
read-through cache over this origin, so Koios is queried only on cache misses.

- `OUROPASS_CHAIN_API_KEY` is optional — set it for a paid Koios tier; blank uses
  unauthenticated public access.
- **Sovereignty (self-hosted Koios):** running your own Koios instead of trusting the
  public endpoint is planned as a future **admin-UI** setting, not a deploy-time env
  (per the installer-scope boundary: operational/admin config does not belong in
  deploy-time env). Direct `cardano-node` (Local State Query) and `cardano-db-sync`
  adapters were removed.

## Data & backups

Everything persistent is bind-mounted under `./data/`:

- `./data/postgres` — the database. **Back this up** (`pg_dump` or stop + copy).
- `./data/caddy` — TLS certificates and Caddy state.

A cold backup is simply: `docker compose stop && tar czf backup.tgz data/ .env`.
Keep `.env` (it holds `OUROPASS_FIELD_KEY`) safe — without it, encrypted DB fields
cannot be decrypted.

## Operations

```sh
docker compose ps                 # status
docker compose logs -f issuer     # follow issuer logs (JSON)
docker compose restart issuer     # restart one service
docker compose down               # stop + remove containers (data in ./data stays)
```

### Updating

Use the update script — it takes a hot database backup, pulls the new image,
restarts (a few seconds of downtime), waits for health, and prints rollback hints
if the new version is unhealthy:

```sh
cd ouro-pass
./deploy/update.sh                 # update to the newest image for the current tag
./deploy/update.sh --tag 0.3.0     # pin a specific version (no leading "v")
./deploy/update.sh --no-backup     # skip the pre-update backup
```

Backups land in `./backups/` (`db-<ts>.sql.gz` + `env-<ts>.bak` — keep the env
copy, it holds `OUROPASS_FIELD_KEY`). The issuer migrates the DB on startup; `./data`
and `.env` are untouched. Equivalent manual steps: edit `OUROPASS_TAG` in `.env`,
then `docker compose pull && docker compose up -d`.

> **Rollback caveat:** migrations are forward-only. Reverting `OUROPASS_TAG` to an
> older image is safe only if the schema didn't change; if it did, restore the
> pre-update backup:
> `gunzip -c backups/db-<ts>.sql.gz | docker compose exec -T postgres psql -U <user> -d <db>`.

To build the image yourself instead of pulling (from a full checkout):

```sh
docker build -t ghcr.io/cauu/ouro-pass:local .
OUROPASS_TAG=local docker compose up -d
```

## Behind an existing reverse proxy (no bundled Caddy)

If the host already runs a reverse proxy on ports 80/443 (e.g. nginx), the bundled
Caddy can't bind those ports. Use **external proxy mode**: the installer runs the
issuer on a local host port and lets *your* proxy terminate TLS and forward to it.

```sh
# interactive — pick "external" when asked, or:
sh install.sh --proxy external
# non-interactive:
... install.sh | OURO_PROXY_MODE=external OURO_DOMAIN=pass.example.com OURO_OWNER_ADDR=stake1... sh -s -- --non-interactive
```

Optional knobs: `OURO_HTTP_PORT` (default `8080`) and `OURO_BIND_ADDR` (default
`127.0.0.1`, i.e. only reachable from the host — correct for a same-host nginx).

What external mode does — and deliberately does **not** do:

- Writes `docker-compose.override.yml` that publishes the issuer on
  `${OURO_BIND_ADDR}:${OURO_HTTP_PORT}` and moves Caddy into an inactive compose
  profile, so `docker compose up -d` starts **only postgres + issuer**. The committed
  `docker-compose.yml` is untouched, so `deploy/update.sh` keeps working unchanged.
- Generates `deploy/ouro-pass.nginx.conf` — a **reference**, **HTTP-only** proxy server
  block with the required forwarded headers. It is intentionally HTTP-only so it passes
  `nginx -t` before any cert exists; `certbot --nginx` then upgrades it to HTTPS for you.
  It does **not** edit your nginx config or obtain certificates; TLS stays under your control.
- After start, it probes `http://<bind>:<port>/healthz` locally and prints the
  remaining operator steps (it never claims public readiness it can't verify).

Prerequisites: install certbot with its nginx plugin (`apt install certbot
python3-certbot-nginx`, or the snap), and open ports **80 and 443** to the internet —
in both the host firewall (e.g. `ufw allow 80,443/tcp`) **and** any cloud security group.

Finish the wiring yourself (the installer prints these):

```sh
sudo cp deploy/ouro-pass.nginx.conf /etc/nginx/conf.d/<domain>.conf
sudo nginx -t && sudo systemctl reload nginx   # HTTP-only block — loads with no cert yet
sudo certbot --nginx -d <domain>               # obtains the cert, adds 443 + HTTP→HTTPS redirect
curl -fsS https://<domain>/healthz             # verify, then open https://<domain>/admin
```

> `certbot --nginx` copies this server block to a new TLS (443) server, injects the
> certificate, and adds the HTTP→HTTPS redirect — so you don't hand-write the 443 block.
> The issuer trusts `X-Forwarded-Proto`/`X-Forwarded-For` (it runs with `OUROPASS_TLS=true`
> + `OUROPASS_TRUSTED_PROXY=true`), and the generated config already sets those headers
> (they carry into the 443 server certbot creates). Already have a cert? Add your own
> `listen 443 ssl;` + `ssl_certificate` lines instead of running certbot.

To switch back to bundled Caddy later, delete `docker-compose.override.yml` and
re-run `docker compose up -d` (Caddy needs 80/443 free).

## External database

To use an existing Postgres instead of the bundled one: comment out the whole
`postgres:` service and the issuer's `depends_on: postgres` in
`docker-compose.yml`, then set your DSN in `.env`:

```
# docker-compose.yml derives OUROPASS_DB_DSN; for an external DB, set it yourself
# in .env (env_file is loaded before the derived value, so also remove the
# OUROPASS_DB_DSN line from the issuer `environment:` block).
OUROPASS_DB_DSN=postgres://user:pass@db.example.com:5432/ouropass?sslmode=require
```

## Local / no-domain testing

Caddy's automatic HTTPS needs a real public domain. For local development without
one, skip compose and run the issuer directly: `cd server && make dev` (SQLite +
mock chain), then open `http://localhost:8080`.

## Troubleshooting

- **Caddy can't get a certificate** — confirm `DOMAIN` resolves to this host and
  ports 80 + 443 are open to the internet. Check `docker compose logs caddy`.
- **Owner can't sign in** — `OUROPASS_OWNER_KEYS` must contain the stake-key hash
  for the wallet you sign with (`make stake-hash ADDR=...`). Restart issuer after
  changing `.env`.
- **issuer keeps restarting / unhealthy** — `docker compose logs issuer`. Common
  causes: empty `OUROPASS_FIELD_KEY`/`OUROPASS_SERVER_SALT`, or Postgres not ready
  (it should gate via healthcheck).
- **No members / chain errors** — verify that each attestor's **network** (set in
  `/admin`) matches the wallet/pool you expect, and that the public Koios endpoint for
  that network (`api`/`preprod`/`preview.koios.rest`) is reachable from the issuer.
- **"Unfinished install detected" / a previous run was interrupted** — the installer
  writes `.env` before the questions, so quitting mid-setup leaves a placeholder `.env`.
  On the next run it detects this (no `.ouro-configured` marker yet) and offers to
  re-configure; non-interactively, pass `--reconfigure`. To start completely fresh
  instead, delete `.env` (nothing is deployed until you finish, so `./data` is empty).

## Hardening notes (optional)

- The runtime image is Alpine + non-root for easy ops/healthcheck. For a more
  minimal attack surface, switch the final stage to `gcr.io/distroless/static`
  (drop the wget HEALTHCHECK or ship a small health binary).
- Pin base images by digest for fully reproducible builds.
- Restrict inbound to 80/443 (and SSH) at the firewall; the issuer and postgres
  ports are not published to the host.
- Consider Docker secrets / a secrets manager instead of `.env` for `FIELD_KEY`.
