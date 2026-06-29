#!/usr/bin/env sh
# Ouro Pass one-line installer (S0011).
#
#   curl -fsSL https://raw.githubusercontent.com/cauu/ouro-pass/main/deploy/install.sh | sh
#
# Downloads the compose stack, generates secrets, asks a few questions, writes .env,
# and (optionally) starts everything. Prefer pinning a release ref:
#   curl -fsSL https://raw.githubusercontent.com/cauu/ouro-pass/v0.2.0/deploy/install.sh | OURO_REF=v0.2.0 sh
#
# Non-interactive (automation): pass --non-interactive plus OURO_* env vars, e.g.
#   curl -fsSL .../install.sh | OURO_DOMAIN=pass.example.com OURO_OWNER_ADDR=stake1... \
#     sh -s -- --non-interactive
#
# Security: review before running —
#   curl -fsSLO .../install.sh && less install.sh && sh install.sh
set -eu

# ── settings (override via env) ──────────────────────────────────────────────
OURO_REF="${OURO_REF:-main}"                       # git ref to fetch files from
OURO_DIR="${OURO_DIR:-ouro-pass}"                  # install directory
OURO_BASEURL="${OURO_BASEURL:-https://raw.githubusercontent.com/cauu/ouro-pass/${OURO_REF}}"
IMAGE="ghcr.io/cauu/ouro-pass"
NONINTERACTIVE="${OURO_NONINTERACTIVE:-0}"
# Reverse-proxy mode: 'caddy' (bundled, auto-HTTPS on 80/443) or 'external' (run behind
# an existing reverse proxy, e.g. nginx). external mode publishes the issuer on a local
# host port instead and emits an nginx snippet — it never touches your host proxy/TLS.
OURO_PROXY_MODE="${OURO_PROXY_MODE:-caddy}"
OURO_HTTP_PORT="${OURO_HTTP_PORT:-8080}"           # external mode: host port for the issuer
OURO_BIND_ADDR="${OURO_BIND_ADDR:-127.0.0.1}"      # external mode: bind address for that port

info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
on_exit() {
  st=$?
  [ "$st" -ne 0 ] && printf '\033[1;31minstall failed (exit %s)\033[0m\n' "$st" >&2
  return 0
}
trap on_exit EXIT

usage() {
  cat <<USAGE
Ouro Pass installer

Usage: install.sh [--non-interactive|-y] [--proxy MODE] [--ref REF] [--dir DIR]

Options:
  --non-interactive, -y   no prompts; take values from OURO_* env vars
  --proxy MODE            reverse proxy: 'caddy' (bundled TLS, default) or 'external'
                          (run behind your own nginx/proxy; no 80/443 needed)
  --ref REF               git ref to download files from (default: ${OURO_REF})
  --dir DIR               install directory (default: ${OURO_DIR})
  -h, --help              show this help

Non-interactive env vars: OURO_DOMAIN, OURO_ACME_EMAIL, OURO_NETWORK,
  OURO_CHAIN_KIND, OURO_KOIOS_BASE_URL, OURO_OWNER_ADDR (or OURO_OWNER_KEYS),
  OUROPASS_TAG, OURO_START (yes|no)

Reverse-proxy env vars: OURO_PROXY_MODE (caddy|external), and for external mode
  OURO_HTTP_PORT (default ${OURO_HTTP_PORT}), OURO_BIND_ADDR (default ${OURO_BIND_ADDR}).

Channels (Telegram, …) are configured in the admin console (/admin) after deploy,
not here — each is a first-class instance with its own token stored in the DB.
USAGE
}

# ── parse flags ──────────────────────────────────────────────────────────────
while [ $# -gt 0 ]; do
  case "$1" in
    --non-interactive|-y) NONINTERACTIVE=1 ;;
    --proxy) shift; OURO_PROXY_MODE="${1:?--proxy needs a value (caddy|external)}" ;;
    --ref) shift; OURO_REF="${1:?--ref needs a value}"; OURO_BASEURL="https://raw.githubusercontent.com/cauu/ouro-pass/${OURO_REF}" ;;
    --dir) shift; OURO_DIR="${1:?--dir needs a value}" ;;
    -h|--help) usage; trap - EXIT; exit 0 ;;
    *) err "unknown argument: $1 (see --help)" ;;
  esac
  shift
done

# Validate reverse-proxy mode early (before any work).
case "$OURO_PROXY_MODE" in
  caddy|external) ;;
  *) err "invalid --proxy / OURO_PROXY_MODE: '$OURO_PROXY_MODE' (use 'caddy' or 'external')" ;;
esac

# ── preflight ────────────────────────────────────────────────────────────────
need() { command -v "$1" >/dev/null 2>&1 || err "$1 is required but not found. $2"; }
info "Checking prerequisites"
need curl "Install curl and retry."
need openssl "Install openssl and retry."
need docker "Install Docker: https://docs.docker.com/engine/install/"
docker compose version >/dev/null 2>&1 || err "Docker Compose v2 plugin is required ('docker compose')."

# ── interactivity guard ──────────────────────────────────────────────────────
# When piped (curl | sh) stdin is the script, so prompts must read /dev/tty.
if [ "$NONINTERACTIVE" != "1" ] && { [ ! -e /dev/tty ] || ! (: >/dev/tty) 2>/dev/null; }; then
  err "no terminal available for prompts. Re-run with --non-interactive and OURO_* env vars (see --help)."
fi

# ── download files (pinned to OURO_REF) ──────────────────────────────────────
info "Installing into ./${OURO_DIR} (ref: ${OURO_REF})"
mkdir -p "$OURO_DIR/deploy"
cd "$OURO_DIR"
fetch() { curl -fsSL "$OURO_BASEURL/$1" -o "$1" || err "download failed: $OURO_BASEURL/$1"; }
fetch docker-compose.yml
fetch .env.example
fetch deploy/Caddyfile
fetch deploy/init.sh
fetch deploy/update.sh
chmod +x deploy/init.sh deploy/update.sh

# ── secrets + ./data (reuse init.sh; idempotent, never overwrites) ───────────
# Detect a re-run BEFORE init.sh creates .env, so we never clobber existing config.
ENV_PREEXISTED=0
[ -f .env ] && ENV_PREEXISTED=1
info "Generating secrets and data directories"
sh deploy/init.sh >/dev/null

# ── collect configuration ────────────────────────────────────────────────────
# ask VAR "prompt" "default" [required]
ask() {
  _var="$1"; _msg="$2"; _def="${3:-}"; _req="${4:-}"
  if [ "$NONINTERACTIVE" = "1" ]; then
    _val="$_def"
  else
    if [ -n "$_def" ]; then printf '%s [%s]: ' "$_msg" "$_def" >/dev/tty
    else printf '%s: ' "$_msg" >/dev/tty; fi
    IFS= read -r _val </dev/tty || _val=""
    [ -z "$_val" ] && _val="$_def"
  fi
  if [ "$_req" = "required" ] && [ -z "$_val" ]; then
    err "a value for \"$_msg\" is required"
  fi
  eval "$_var=\$_val"
}

# set_env KEY VALUE — overwrite (or append) KEY in .env
set_env() {
  _k="$1"; _v="$2"; _t="$(mktemp)"
  awk -v k="$_k" -v v="$_v" '
    !done && $0 ~ "^"k"=" { print k"="v; done=1; next }
    { print }
    END { if (!done) print k"="v }
  ' .env > "$_t" && mv "$_t" .env
}

if [ "$ENV_PREEXISTED" = "1" ]; then
  # Re-run on an existing install: keep the operator's config, don't clobber it.
  warn "existing .env found — keeping your configuration (only missing secrets were filled)."
  warn "to change settings, edit .env directly or use deploy/update.sh."
  DOMAIN="$(sed -n 's/^DOMAIN=//p' .env | head -n1)"
else
  info "Configuration"
  ask DOMAIN "Public domain (must resolve to this host)" "${OURO_DOMAIN:-}" required
  if [ "$OURO_PROXY_MODE" = "caddy" ]; then
    ask ACME_EMAIL "ACME/Let's Encrypt email (optional)" "${OURO_ACME_EMAIL:-}"
  else
    ACME_EMAIL=""   # external proxy terminates TLS; bundled Caddy/ACME is unused
  fi
  ask NETWORK "Cardano network (mainnet|preprod|preview)" "${OURO_NETWORK:-preprod}"
  case "$NETWORK" in
    mainnet) KOIOS_DEF="https://api.koios.rest/api/v1" ;;
    preview) KOIOS_DEF="https://preview.koios.rest/api/v1" ;;
    *)       KOIOS_DEF="https://preprod.koios.rest/api/v1" ;;
  esac
  ask CHAIN_KIND "Chain data source (koios|blockfrost|node_lsq|db_sync|mock)" "${OURO_CHAIN_KIND:-koios}"
  ask KOIOS_BASE_URL "Koios base URL" "${OURO_KOIOS_BASE_URL:-$KOIOS_DEF}"
  ask TAG "Image tag (e.g. 0.1.0, no leading v; or latest)" "${OUROPASS_TAG:-latest}"
  case "$TAG" in v[0-9]*) TAG="${TAG#v}" ;; esac   # image tags have no leading 'v'
  ask OWNER_ADDR "Owner stake address (stake1...) to admit as admin owner" "${OURO_OWNER_ADDR:-}"

  # Owner key hash: from stake address (via the image) or a precomputed value.
  OWNER_KEYS="${OURO_OWNER_KEYS:-}"
  if [ -z "$OWNER_KEYS" ] && [ -n "$OWNER_ADDR" ]; then
    info "Computing owner key hash from $OWNER_ADDR"
    OWNER_KEYS="$(docker run --rm "$IMAGE:$TAG" stake-hash "$OWNER_ADDR")" \
      || err "could not compute stake hash (check the address / image tag)"
  fi
  [ -z "$OWNER_KEYS" ] && warn "no owner key set — set OUROPASS_OWNER_KEYS in .env before you can sign in to /admin"

  # ── write .env ─────────────────────────────────────────────────────────────
  info "Writing .env"
  set_env DOMAIN "$DOMAIN"
  set_env ACME_EMAIL "$ACME_EMAIL"
  set_env OUROPASS_TAG "$TAG"
  set_env OUROPASS_NETWORK "$NETWORK"
  set_env OUROPASS_CHAIN_KIND "$CHAIN_KIND"
  set_env OUROPASS_KOIOS_BASE_URL "$KOIOS_BASE_URL"
  set_env OUROPASS_OWNER_KEYS "$OWNER_KEYS"

  # Caddy errors on an empty `email` directive, so only enable it when provided.
  # External-proxy mode never runs Caddy, so skip the Caddyfile email block entirely.
  if [ "$OURO_PROXY_MODE" = "caddy" ] && [ -n "$ACME_EMAIL" ] && ! grep -q '^[[:space:]]*email ' deploy/Caddyfile; then
    # literal {$ACME_EMAIL} is a Caddy env placeholder, resolved at runtime — keep single quotes.
    # shellcheck disable=SC2016
    printf '{\n\temail {$ACME_EMAIL}\n}\n\n%s\n' "$(cat deploy/Caddyfile)" > deploy/Caddyfile.tmp \
      && mv deploy/Caddyfile.tmp deploy/Caddyfile
    info "Enabled Caddy ACME email block"
  fi
fi

# ── external reverse-proxy mode: compose override ────────────────────────────
# Publish the issuer on a local host port and disable the bundled Caddy by moving
# it into an inactive compose profile, so `docker compose up -d` skips it. This is
# a generated override (compose auto-loads docker-compose.override.yml); the
# committed docker-compose.yml is never edited, so update.sh keeps working.
if [ "$OURO_PROXY_MODE" = "external" ]; then
  if [ -f docker-compose.override.yml ]; then
    warn "docker-compose.override.yml exists — keeping it (not regenerating)."
  else
    info "Writing docker-compose.override.yml (publish issuer on ${OURO_BIND_ADDR}:${OURO_HTTP_PORT}, disable caddy)"
    cat > docker-compose.override.yml <<YAML
# Generated by install.sh --proxy external. Do not commit secrets here.
# Publishes the issuer on a local host port for your own reverse proxy (e.g. nginx)
# and disables the bundled Caddy by assigning it an inactive profile, so a plain
# 'docker compose up -d' starts only postgres + issuer. Re-enable Caddy by deleting
# this file. The committed docker-compose.yml is untouched.
services:
  issuer:
    ports:
      - "${OURO_BIND_ADDR}:${OURO_HTTP_PORT}:8080"
  caddy:
    profiles: ["caddy-disabled"]
YAML
  fi

  # nginx reverse-proxy reference — generated, never applied to the host.
  if [ -f deploy/ouro-pass.nginx.conf ]; then
    warn "deploy/ouro-pass.nginx.conf exists — keeping it (not regenerating)."
  else
    info "Writing deploy/ouro-pass.nginx.conf (reference config for your nginx)"
    # Unquoted heredoc: ${DOMAIN}/${OURO_*} expand; nginx \$vars stay literal.
    cat > deploy/ouro-pass.nginx.conf <<NGINX
# Ouro Pass — nginx reverse proxy (generated by install.sh --proxy external).
# This is a REFERENCE you adapt to your host; install.sh never edits nginx or certs.
#
# TLS, two common paths:
#   • Place an HTTP-only version of this first, then run:
#       certbot --nginx -d ${DOMAIN}
#     certbot adds the 443 listener, cert lines, and redirect for you.
#   • Already have a certificate? Keep the 443 block and point ssl_certificate at it.
# Note: the 443 block assumes certs already exist at the certbot default path, so
# \`nginx -t\` fails until they do (run certbot first, or use the HTTP-only path above).

server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN};
    location / { return 301 https://\$host\$request_uri; }
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name ${DOMAIN};

    ssl_certificate     /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;

    location / {
        proxy_pass http://${OURO_BIND_ADDR}:${OURO_HTTP_PORT};
        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
NGINX
  fi
fi

# ── optionally start ─────────────────────────────────────────────────────────
ask START "Start the stack now? (yes/no)" "${OURO_START:-yes}"
case "$START" in
  y|Y|yes|YES|true|1)
    info "Starting: docker compose up -d"
    # Run inside an `if` so `set -e` doesn't abort before we can explain a failure.
    if docker compose up -d; then
      info "Done. Open https://${DOMAIN}/admin and sign in with your owner wallet."
      info "Add delivery channels (Telegram, …) from the admin console after signing in."
    else
      st=$?
      warn "docker compose up -d failed (exit ${st})."
      if [ "$OURO_PROXY_MODE" = "caddy" ]; then
        # The usual cause is 80/443 already in use (e.g. an existing nginx). compose is
        # not transactional: postgres/issuer may already be running — a normal partial
        # state, not data loss (./data persists). See README "behind an existing proxy".
        warn "Ports 80/443 may be in use (e.g. an existing reverse proxy)."
        warn "postgres/issuer may already be running — that is an expected partial state,"
        warn "not data loss; ./data is intact."
        warn "Fix and finish one of two ways:"
        warn "  • free 80/443, then re-run (idempotent):  docker compose up -d"
        warn "  • redeploy behind your existing proxy:    install.sh --proxy external"
      fi
      exit "${st}"
    fi
    ;;
  *)
    info "Skipped start. When ready:  cd ${OURO_DIR} && docker compose up -d"
    ;;
esac

trap - EXIT
