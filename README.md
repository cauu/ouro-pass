# Ouro Pass

**Self-hosted staking-identity issuer for Cardano stake-pool operators.** Ouro Pass
turns on-chain stake (pool membership / active delegation) into **OAuth logins** and
**multi-channel memberships** — without the service ever holding a user's keys.

A wallet proves control via a CIP-30 signature; the issuer mints a short-lived,
JWKS-verifiable JWT and computes the holder's **tier** from live on-chain facts.
Everything ships as **one Go binary** (API + admin console + background workers) and
deploys with a single `docker compose up -d`.

---

## Highlights

- **Staking-identity OAuth 2.0** — `authorization_code` + **PKCE**, `refresh_token`,
  token `introspect` (RFC 7662) / `revoke` (RFC 7009); short-lived JWTs verified via
  published **JWKS**, with rotatable signing keys.
- **Wallet auth, no custody** — CIP-30 `signData` (COSE) challenge/response. The
  service holds only its issuer signing key and bot tokens; never user/cold/owner keys.
- **Eligibility & tiers from chain** — a rule engine evaluates tiers over live active
  stake read through **Koios**, the single read-only chain origin (built-in public
  per-network endpoints; an in-memory `MockSource` is injected for tests).
- **Multi-channel delivery** — Telegram channel instances, push broadcasts, and a
  per-epoch reconciliation job that re-evaluates and downgrades/expires sessions.
- **Admin console** — embedded SPA at `/admin`: grouped navigation, RBAC
  (`viewer` < `operator` < `owner`), owner-key wallet login, step-up re-signing for
  sensitive actions, and an immutable audit log.
- **Single binary, your data** — Postgres (production) or SQLite (single-host);
  schema auto-migrates on startup; all persistent state bind-mounted to `./data`.
- **One-command deploy** — bundled Postgres + Caddy (automatic HTTPS).

## Architecture

The issuer is **one binary** exposing four auth-independent planes (wallet primitives /
issuance / verifier / admin) plus the Telegram and scheduler workers, and serving the
admin SPA — all baked in via `go:embed`.

```
                 ┌─────────────────────────── your host ───────────────────────────┐
   Internet ──▶  caddy (TLS, :80/:443)  ──▶  issuer (:8080, API + /admin + workers)
                                                     │
                                                     ▼
                                              postgres (:5432)
   persistent state (host bind-mounts):  ./data/postgres   ./data/caddy
   read-only chain queries:  issuer ──▶ koios (public per-network endpoints)
```

## Quick start (deploy)

One command — it checks prerequisites, downloads the compose stack, generates
secrets, asks a few questions (domain, owner wallet) and starts
everything:

```sh
curl -fsSL https://raw.githubusercontent.com/cauu/ouro-pass/main/deploy/install.sh | sh
```

Inspect-before-run (recommended) or automate it non-interactively:

```sh
# review first
curl -fsSLO https://raw.githubusercontent.com/cauu/ouro-pass/main/deploy/install.sh
less install.sh && sh install.sh

# non-interactive (CI / scripted)
curl -fsSL https://raw.githubusercontent.com/cauu/ouro-pass/main/deploy/install.sh \
  | OURO_DOMAIN=pass.example.com OURO_OWNER_ADDR=stake1... sh -s -- --non-interactive
```

You don't need the source tree — the installer fetches only the compose stack
(`docker-compose.yml`, `.env.example`, `deploy/Caddyfile`, `deploy/init.sh`,
`deploy/update.sh`).

<details><summary>Or set it up manually</summary>

```sh
mkdir -p ouro-pass/deploy && cd ouro-pass
BASE=https://raw.githubusercontent.com/cauu/ouro-pass/main
curl -fsSLO $BASE/docker-compose.yml
curl -fsSLO $BASE/.env.example
curl -fsSL  $BASE/deploy/Caddyfile -o deploy/Caddyfile
curl -fsSL  $BASE/deploy/init.sh   -o deploy/init.sh
curl -fsSL  $BASE/deploy/update.sh -o deploy/update.sh
chmod +x deploy/init.sh deploy/update.sh

./deploy/init.sh                 # generate .env + random secrets + ./data dirs
docker run --rm ghcr.io/cauu/ouro-pass stake-hash stake1...   # your owner key hash
$EDITOR .env                     # set DOMAIN + OUROPASS_OWNER_KEYS (and review the rest)
docker compose up -d             # pull image + start issuer + postgres + caddy
# → open https://<DOMAIN>/admin and sign in with your owner wallet
```
</details>

By default the stack includes **Caddy** for automatic HTTPS on ports 80/443. Already
running nginx (or another proxy) there? Install with `--proxy external` (or
`OURO_PROXY_MODE=external`): the issuer is published on a local port and the installer
emits a reference nginx config for you to wire up — see
[docs/deployment.md](docs/deployment.md#behind-an-existing-reverse-proxy-no-bundled-caddy).

The image is published multi-arch (linux/amd64 + linux/arm64) to GHCR:

```
ghcr.io/cauu/ouro-pass:0.1.0     # pin a version (recommended)
ghcr.io/cauu/ouro-pass:latest
```

Full guide — prerequisites, the Koios chain source, backups, upgrades,
troubleshooting — in **[docs/deployment.md](docs/deployment.md)**.

### Updating

```sh
cd ouro-pass
./deploy/update.sh                 # hot backup → pull → restart → health-check
./deploy/update.sh --tag 0.3.0     # pin a specific version
```

A short restart blip is expected. Backups land in `./backups/`; if a new version is
unhealthy the script prints rollback steps. See [docs/deployment.md](docs/deployment.md#updating).

## Run from source / development

```sh
# backend: run the issuer locally (SQLite + mock chain) at http://localhost:8080
cd server && make dev

# admin SPA dev server (proxied during development)
cd web && pnpm install && pnpm dev

# build the SPA into the binary, then compile the embedded issuer
cd server && make web && make build

# tests
cd server && make test            # unit + e2e (SQLite + mocks)
cd server && make test-integration  # Postgres dialect/concurrency (needs Docker or a DSN)
cd web && pnpm test

# compute an owner stake-key hash for OUROPASS_OWNER_KEYS
cd server && make stake-hash ADDR=stake1...
```

## Configuration

All runtime config is environment-driven (`OUROPASS_*`); secrets (DB DSN, AES field
key, HMAC salt, chain API key, bot token) arrive via env only — never committed, never
stored in the DB or image. See **[.env.example](.env.example)** for the annotated list
and [docs/deployment.md](docs/deployment.md) for what each knob does.

## Tech stack

- **Backend** — Go 1.25, `chi` router + stdlib `net/http`; `pgx` (Postgres) /
  `modernc.org/sqlite` (pure-Go SQLite); `lestrrat-go/jwx` (JWT/JWKS),
  `veraison/go-cose` + `fxamacker/cbor` (CIP-30 COSE).
- **Admin SPA** — React 18 + Vite 6 + TypeScript (strict) + Tailwind v4 + Radix
  (shadcn-style) + TanStack Query/Table + React Hook Form + Zod.
- **Delivery** — multi-arch Docker image, Caddy (automatic HTTPS), Docker Compose.

## Repository layout

```
server/                 Go module (issuer binary, workers, planes, migrations)
  cmd/issuer/           service entrypoint (also: `issuer stake-hash <addr>`)
  internal/             config, core services, store, httpapi (+ embedded adminui), workers
web/                    admin SPA (embedded into the binary at build time)
deploy/                 Caddyfile, init.sh (secret bootstrap)
docs/                   design docs, deployment guide, and specs/
Dockerfile              multi-stage build (SPA → Go embed → minimal runtime)
docker-compose.yml      issuer + postgres + caddy
.github/workflows/      ci (test/lint/build) + release (tag → multi-arch GHCR push)
```

## Development process

Work is tracked with **append-only specifications** under `docs/specs/` (one active
spec at a time; finished specs move to `docs/specs/completed/`). Each change maps to a
spec item and is committed as `spec(<spec-file>): <item-id> <action>`.

## Security notes

- The service holds only its **issuer signing key** (encrypted at rest) and bot
  tokens; cold / owner / payment / KES / VRF keys never enter it.
- Sensitive 🔒 fields (signing key, bot token, client secret) are encrypted with the
  `OUROPASS_FIELD_KEY` (AES-256-GCM); the public `sub` is a salted HMAC pseudonym.
- Releases are built reproducibly from pinned base images and run as a non-root user.

## License

No license file yet — add a `LICENSE` before public distribution. Until then, all
rights are reserved by default.
