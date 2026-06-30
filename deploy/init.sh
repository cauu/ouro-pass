#!/usr/bin/env sh
# Ouro Pass deployment bootstrap (S0009).
#
# Idempotent: creates .env from .env.example (if missing), fills any EMPTY secret
# with a strong random value, and creates the host-mounted ./data directories.
# Existing non-empty values are never overwritten. Safe to re-run.
#
# Usage:  ./deploy/init.sh
# Needs:  openssl
set -eu

# Resolve repo root (script lives in deploy/).
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

command -v openssl >/dev/null 2>&1 || { echo "error: openssl is required" >&2; exit 1; }

if [ ! -f .env.example ]; then
  echo "error: .env.example not found in $ROOT" >&2
  exit 1
fi

if [ -f .env ]; then
  echo ".env exists — filling only empty secrets (nothing overwritten)."
else
  cp .env.example .env
  echo "created .env from .env.example"
fi

# fill_if_empty KEY VALUE — set KEY=VALUE only when the current value is empty.
fill_if_empty() {
  key="$1"; val="$2"
  cur="$(sed -n "s/^${key}=//p" .env | head -n1)"
  if [ -n "$cur" ]; then
    echo "  ${key}: kept (already set)"
    return 0
  fi
  tmp="$(mktemp)"
  # Replace the first "KEY=" line; awk avoids sed delimiter issues with the value.
  awk -v k="$key" -v v="$val" '
    !done && $0 ~ "^"k"=" { print k"="v; done=1; next }
    { print }
    END { if (!done) print k"="v }
  ' .env > "$tmp"
  mv "$tmp" .env
  echo "  ${key}: generated"
}

echo "Generating secrets (empty ones only):"
fill_if_empty OUROPASS_FIELD_KEY   "$(openssl rand -hex 32)"   # AES-256 master key (32 bytes)
fill_if_empty OUROPASS_SERVER_SALT "$(openssl rand -hex 16)"   # HMAC salt (16 bytes)
fill_if_empty POSTGRES_PASSWORD    "$(openssl rand -hex 24)"   # Postgres password

echo "Creating host data directories under ./data:"
mkdir -p data/postgres data/caddy/config
echo "  ./data/postgres  (Postgres data — back this up)"
echo "  ./data/caddy      (Caddy TLS certs/state — back this up)"

cat <<'NEXT'

Done. Next steps:
  1) Edit .env and set at least:
       DOMAIN              your public domain (must resolve to this host)
       OUROPASS_OWNER_KEYS your owner stake-key hash(es) to sign in to /admin
                           (cd server && make stake-hash ADDR=stake1...)
     Review OUROPASS_CHAIN_KIND (network is per-attestor, set in /admin).
  2) Start the stack:
       docker compose up -d
  3) Open https://<DOMAIN>/admin and sign in with your owner wallet.

See docs/deployment.md for the full guide.
NEXT
