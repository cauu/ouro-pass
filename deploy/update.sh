#!/usr/bin/env sh
# Ouro Pass update script (S0012).
#
# Simple and reliable for self-hosted/decentralized operators: it tolerates a few
# seconds of restart downtime in exchange for a hot backup, version pinning, a
# health check, and clear rollback hints. Run from the install directory:
#
#   ./deploy/update.sh                 # back up, pull, restart, health-check
#   ./deploy/update.sh --tag 0.3.0     # bump OUROPASS_TAG, then update
#   ./deploy/update.sh --no-backup     # skip the pg_dump backup
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BACKUP=1
NEWTAG=""
IMAGE="ghcr.io/cauu/ouro-pass"

info() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
err()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }
# shellcheck disable=SC2329  # invoked indirectly via trap
on_exit() {
  st=$?
  [ "$st" -ne 0 ] && printf '\033[1;31mupdate failed (exit %s)\033[0m\n' "$st" >&2
  return 0
}
trap on_exit EXIT

usage() {
  cat <<USAGE
Ouro Pass updater — back up, pull the new image, restart, health-check.

Usage: deploy/update.sh [--tag TAG] [--no-backup]

  --tag TAG     set OUROPASS_TAG=TAG in .env before updating (e.g. 0.3.0; no leading v)
  --no-backup   skip the pre-update Postgres backup
  -h, --help    show this help
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --tag) shift; NEWTAG="${1:?--tag needs a value}" ;;
    --no-backup) BACKUP=0 ;;
    -h|--help) usage; trap - EXIT; exit 0 ;;
    *) err "unknown argument: $1 (see --help)" ;;
  esac
  shift
done

# ── preflight ────────────────────────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || err "docker is required"
docker compose version >/dev/null 2>&1 || err "Docker Compose v2 plugin is required"
[ -f docker-compose.yml ] || err "docker-compose.yml not found in $ROOT (run from the install dir)"
[ -f .env ] || err ".env not found in $ROOT"

get_env() { sed -n "s/^$1=//p" .env | head -n1; }
PGUSER="$(get_env POSTGRES_USER)"; PGUSER="${PGUSER:-ouropass}"
PGDB="$(get_env POSTGRES_DB)";     PGDB="${PGDB:-ouropass}"
OLD_TAG="$(get_env OUROPASS_TAG)";  OLD_TAG="${OLD_TAG:-latest}"

set_env() {
  _k="$1"; _v="$2"; _t="$(mktemp)"
  awk -v k="$_k" -v v="$_v" '
    !done && $0 ~ "^"k"=" { print k"="v; done=1; next }
    { print }
    END { if (!done) print k"="v }
  ' .env > "$_t" && mv "$_t" .env
}

pg_running() {
  _cid="$(docker compose ps -q postgres 2>/dev/null || true)"
  [ -n "$_cid" ] && [ "$(docker inspect -f '{{.State.Running}}' "$_cid" 2>/dev/null || echo false)" = "true" ]
}

# ── 1) hot backup (default) ──────────────────────────────────────────────────
BACKUP_FILE=""
if [ "$BACKUP" = "1" ]; then
  if pg_running; then
    ts="$(date +%Y%m%dT%H%M%S)"
    mkdir -p backups
    info "Backing up database (hot pg_dump)"
    if docker compose exec -T postgres pg_dump -U "$PGUSER" "$PGDB" > "backups/db-$ts.sql"; then
      gzip -f "backups/db-$ts.sql"
      cp .env "backups/env-$ts.bak"
      BACKUP_FILE="backups/db-$ts.sql.gz"
      info "Backup: $BACKUP_FILE (+ backups/env-$ts.bak — keep it; it holds OUROPASS_FIELD_KEY)"
    else
      rm -f "backups/db-$ts.sql"
      err "pg_dump failed; aborting update (use --no-backup to override)"
    fi
  else
    err "postgres is not running — cannot take a pre-update backup. Start it (docker compose up -d postgres) or re-run with --no-backup."
  fi
else
  warn "skipping backup (--no-backup)"
fi

# ── 2) pin version if requested ──────────────────────────────────────────────
if [ -n "$NEWTAG" ]; then
  info "Validating image $IMAGE:$NEWTAG"
  docker pull "$IMAGE:$NEWTAG" >/dev/null \
    || err "image $IMAGE:$NEWTAG not found — leaving .env unchanged (use a published tag, no leading 'v')"
  info "Setting OUROPASS_TAG=$NEWTAG (was: $OLD_TAG)"
  set_env OUROPASS_TAG "$NEWTAG"
fi

# ── 3) pull + restart (brief downtime accepted) ──────────────────────────────
info "Pulling images"
docker compose pull
info "Applying update (containers restart — brief downtime)"
docker compose up -d

# ── 4) health check ──────────────────────────────────────────────────────────
info "Waiting for issuer to become healthy"
cid="$(docker compose ps -q issuer 2>/dev/null || true)"
[ -n "$cid" ] || err "issuer container not found after 'up'"
i=0
healthy=0
while [ "$i" -lt 45 ]; do   # ~90s
  s="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$cid" 2>/dev/null || echo unknown)"
  case "$s" in
    healthy|none) healthy=1; break ;;
  esac
  sleep 2; i=$((i + 1))
done

if [ "$healthy" = "1" ]; then
  info "Update complete. issuer is healthy (OUROPASS_TAG=$(get_env OUROPASS_TAG))."
  trap - EXIT
  exit 0
fi

# ── 5) unhealthy → rollback hints ────────────────────────────────────────────
warn "issuer did not become healthy within ~90s. Recent logs:"
docker compose logs --tail 30 issuer >&2 || true
cat >&2 <<ROLLBACK

Roll back:
  1) restore the previous image tag:
       sed -i.bak 's/^OUROPASS_TAG=.*/OUROPASS_TAG=${OLD_TAG}/' .env && docker compose up -d
  2) if the database must be restored from the pre-update backup:
       gunzip -c ${BACKUP_FILE:-backups/db-<timestamp>.sql.gz} \\
         | docker compose exec -T postgres psql -U ${PGUSER} -d ${PGDB}
ROLLBACK
exit 1
