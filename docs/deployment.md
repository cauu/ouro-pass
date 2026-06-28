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
- **issuer** is stateless (signing keys live in the DB); built from this repo.
- **postgres** holds all data; bind-mounted to `./data/postgres` so it is visible
  on the host and easy to back up.

## Prerequisites

- Docker Engine 24+ and the Compose v2 plugin (`docker compose version`).
- `openssl` (for `deploy/init.sh`).
- A **public domain** (e.g. `pass.example.com`) with an A/AAAA record pointing at
  this host, and inbound TCP **80 and 443** open (Caddy needs both for ACME).
- A Cardano wallet whose stake key will be the admin **owner**.

## Quick start

```sh
git clone <this-repo> ouro-pass && cd ouro-pass

# 1) generate .env with strong random secrets + create ./data dirs
./deploy/init.sh

# 2) edit .env — set at minimum:
#      DOMAIN=pass.example.com
#      OUROPASS_OWNER_KEYS=<your owner stake-key hash>     (see below)
#    and review OUROPASS_NETWORK / OUROPASS_CHAIN_KIND / OUROPASS_KOIOS_BASE_URL
$EDITOR .env

# 3) build + start everything
docker compose up -d

# 4) open https://<DOMAIN>/admin and sign in with your owner wallet
```

`docker compose up -d` builds the issuer image on first run (a few minutes), then
starts postgres → issuer → caddy in dependency order. The issuer migrates the
database on startup; there is no separate migration step.

### Getting your owner key hash

`OUROPASS_OWNER_KEYS` is a comma-separated list of on-chain owner stake-key
hashes allowed to sign in to `/admin` as owner. From the repo:

```sh
cd server && make stake-hash ADDR=stake1...   # prints the hash to put in OUROPASS_OWNER_KEYS
```

## Configuration reference

All knobs live in `.env` (see `.env.example` for the annotated list). Highlights:

| Variable | Purpose |
|---|---|
| `DOMAIN` | Public domain; Caddy's cert + the token `iss` (`https://${DOMAIN}`). |
| `ACME_EMAIL` | Optional Let's Encrypt contact for expiry notices. |
| `OUROPASS_FIELD_KEY` | **Secret.** AES-256 master key for 🔒 fields (`openssl rand -hex 32`). |
| `OUROPASS_SERVER_SALT` | **Secret.** HMAC salt for the pseudonymous `sub` (`openssl rand -hex 16`). |
| `OUROPASS_NETWORK` | Default network for new attestors: `mainnet`/`preprod`/`preview`. |
| `OUROPASS_CHAIN_KIND` | Chain data source: `koios` (default) / `blockfrost` / `node_lsq` / `db_sync` / `mock`. |
| `OUROPASS_KOIOS_BASE_URL` | Koios endpoint for your network. |
| `OUROPASS_CHAIN_API_KEY` | Optional API key (koios tier / blockfrost project id). |
| `OUROPASS_OWNER_KEYS` | Owner stake-key hashes for `/admin`. |
| `OUROPASS_TELEGRAM_BOT` / `_TOKEN` | Optional Telegram delivery. |
| `POSTGRES_USER` / `_PASSWORD` / `_DB` | Bundled Postgres credentials. |

> Derived automatically in `docker-compose.yml` (do not set in `.env`):
> `OUROPASS_ADDR`, `OUROPASS_ISSUER`, `OUROPASS_TRUSTED_PROXY`, `OUROPASS_TLS`,
> `OUROPASS_DB_DRIVER`, `OUROPASS_DB_DSN`.

> ⚠️ Never reuse the fixed dev keys from `server/Makefile`. Rotating
> `OUROPASS_FIELD_KEY` after data exists makes previously-encrypted 🔒 fields
> unreadable.

## Chain data source (decentralization knob)

`OUROPASS_CHAIN_KIND` chooses how the issuer reads on-chain stake facts — a
trade-off between convenience and sovereignty:

- **`koios` (default)** — a federated, open API. Point `OUROPASS_KOIOS_BASE_URL`
  at a public instance or your own self-hosted Koios. No SaaS lock-in.
- **`blockfrost`** — a hosted SaaS. Easiest to start, but centralized and needs a
  project id in `OUROPASS_CHAIN_API_KEY`.
- **`node_lsq` / `db_sync` (sovereign)** — read from your **own** `cardano-node`
  (Local State Query) or `cardano-db-sync`. Fully self-reliant, but a full node is
  hundreds of GB and takes days to sync. Run the node yourself and set
  `OUROPASS_CHAIN_KIND=node_lsq` + `OUROPASS_NODE_SOCKET`, then mount the node
  socket into the issuer container (add a `volumes:` entry for the socket path).
  Bundling a full node is out of scope for this compose file by design.

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

# update to a new version:
git pull
docker compose build issuer
docker compose up -d
```

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
- **No members / chain errors** — verify `OUROPASS_CHAIN_KIND`,
  `OUROPASS_KOIOS_BASE_URL` matches `OUROPASS_NETWORK`, and the koios instance is
  reachable.

## Hardening notes (optional)

- The runtime image is Alpine + non-root for easy ops/healthcheck. For a more
  minimal attack surface, switch the final stage to `gcr.io/distroless/static`
  (drop the wget HEALTHCHECK or ship a small health binary).
- Pin base images by digest for fully reproducible builds.
- Restrict inbound to 80/443 (and SSH) at the firewall; the issuer and postgres
  ports are not published to the host.
- Consider Docker secrets / a secrets manager instead of `.env` for `FIELD_KEY`.
