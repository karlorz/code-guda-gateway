#!/usr/bin/env bash
# seed-instance.sh — full instance coverage seed (VM / Coolify / local).
#
# Same operational flow as kr01 / secrets.env.example, reusable everywhere:
#   1) db migrate
#   2) admin token  (sync-env if GUDA_ADMIN_TOKEN set, else init once)
#   3) gateway key  (create named key if missing; save raw when created)
#   4) provider endpoints + quota  (delegates to seed-provider-keys.sh)
#
# Usage (VM / bare metal — same as production seed notes):
#   set -a; . ~/.secrets/guda-gateway.env; set +a
#   export DB_PATH=... GUDA_MASTER_KEY_PATH=...
#   ./scripts/seed-instance.sh [/path/to/guda-gateway-admin]
#
# Usage (Coolify / Docker — optional, after first healthy deploy):
#   docker exec -e DB_PATH=... -e GUDA_MASTER_KEY_PATH=... \
#     -e GUDA_ADMIN_TOKEN=... \
#     -e GROK_1_API_KEY=... -e TAVILY_API_KEYS=... -e FIRECRAWL_1_API_KEY=... \
#     <container> seed-instance
#   # or: mount secrets and run the one-shot seed service (docker-compose.coolify-seed.yml)
#
# Flags (env):
#   GUDA_SEED_GATEWAY_KEY_NAME   default: daily (Coolify) / groksearch (if GUDA_SEED_PROFILE=prod)
#   GUDA_SEED_SAVE_ENV           path to write GUDA_ADMIN_TOKEN / GUDA_API_KEY (default under DB dir)
#   GUDA_SEED_SKIP_ADMIN=1       skip admin token step
#   GUDA_SEED_SKIP_GATEWAY_KEY=1 skip gateway key step
#   GUDA_SEED_SKIP_PROVIDERS=1   skip provider seed
#   GUDA_SEED_PROFILE=prod|dev|coolify  naming defaults only
#
# Never prints raw secrets except gateway-key create stdout (once) when a new key is made.
# Never puts secrets on argv for provider-endpoint (stdin / seed-provider-keys.sh).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Repo checkout: scripts/.. ; container: /usr/local/bin → look at share path too.
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ADM="${1:-}"
if [ -z "$ADM" ]; then
  if command -v guda-gateway-admin >/dev/null 2>&1; then
    ADM="$(command -v guda-gateway-admin)"
  elif [ -x "$ROOT/guda-gateway-admin" ]; then
    ADM="$ROOT/guda-gateway-admin"
  else
    ADM="guda-gateway-admin"
  fi
fi

DB="${DB_PATH:?DB_PATH must be set}"
MK="${GUDA_MASTER_KEY_PATH:?GUDA_MASTER_KEY_PATH must be set}"

PROFILE="${GUDA_SEED_PROFILE:-}"
case "$PROFILE" in
  prod|kr01) DEFAULT_GK_NAME="groksearch" ;;
  coolify|docker) DEFAULT_GK_NAME="coolify" ;;
  *) DEFAULT_GK_NAME="daily" ;;
esac
GK_NAME="${GUDA_SEED_GATEWAY_KEY_NAME:-$DEFAULT_GK_NAME}"

CRED_DIR="$(dirname "$DB")"
SAVE_ENV="${GUDA_SEED_SAVE_ENV:-$CRED_DIR/admin-credentials.env}"
SEED_PROVIDERS=""
for candidate in \
  "${SCRIPT_DIR}/seed-provider-keys.sh" \
  "${ROOT}/scripts/seed-provider-keys.sh" \
  "/usr/local/share/code-guda-gateway/seed-provider-keys.sh" \
  "$(dirname "$ADM")/../share/code-guda-gateway/seed-provider-keys.sh"
do
  if [ -f "$candidate" ]; then
    SEED_PROVIDERS="$candidate"
    break
  fi
done

run_adm() {
  "$ADM" --db "$DB" --master-key "$MK" "$@"
}

log() { printf 'seed-instance: %s\n' "$*" >&2; }

# --- 1) migrate --------------------------------------------------------------
log "db migrate"
run_adm db migrate

# --- 2) admin token ----------------------------------------------------------
if [ "${GUDA_SEED_SKIP_ADMIN:-0}" != "1" ]; then
  if [ -n "${GUDA_ADMIN_TOKEN:-}" ]; then
    log "admin token: sync-env → $SAVE_ENV"
    # stdout is the secret; discard from console, still write via --save-env
    run_adm token sync-env --save-env "$SAVE_ENV" >/dev/null
  else
    # init only if empty; ignore "already initialized"
    if run_adm token init --save-env "$SAVE_ENV" >/dev/null 2>/tmp/guda-seed-token-init.err; then
      log "admin token: init → $SAVE_ENV"
    else
      if grep -q 'already initialized' /tmp/guda-seed-token-init.err 2>/dev/null; then
        log "admin token: already initialized (set GUDA_ADMIN_TOKEN + sync-env to align)"
      else
        cat /tmp/guda-seed-token-init.err >&2 || true
        log "admin token: init failed"
        exit 1
      fi
    fi
  fi
  rm -f /tmp/guda-seed-token-init.err
else
  log "admin token: skipped"
fi

# --- 3) gateway key ----------------------------------------------------------
if [ "${GUDA_SEED_SKIP_GATEWAY_KEY:-0}" != "1" ]; then
  if run_adm gateway-key list 2>/dev/null | awk -v n="$GK_NAME" 'NR>1 && $2==n { found=1 } END { exit found ? 0 : 1 }'; then
    log "gateway-key: name=$GK_NAME already exists (skip create)"
  else
    log "gateway-key: create --name $GK_NAME"
    raw="$(run_adm gateway-key create --name "$GK_NAME")"
    raw="$(printf '%s' "$raw" | tr -d '\r' | head -n1)"
    # Append / update GUDA_API_KEY in save-env without echoing to console
    if [ -n "$raw" ]; then
      umask 077
      if [ -f "$SAVE_ENV" ]; then
        tmp="$(mktemp)"
        awk -v v="$raw" '
          BEGIN { done=0 }
          /^GUDA_API_KEY=/ || /^export GUDA_API_KEY=/ {
            print "GUDA_API_KEY=" v; done=1; next
          }
          { print }
          END { if (!done) print "GUDA_API_KEY=" v }
        ' "$SAVE_ENV" >"$tmp"
        mv "$tmp" "$SAVE_ENV"
      else
        printf 'GUDA_API_KEY=%s\n' "$raw" >"$SAVE_ENV"
      fi
      chmod 600 "$SAVE_ENV"
      log "gateway-key: raw saved to $SAVE_ENV as GUDA_API_KEY (not printed)"
    fi
  fi
else
  log "gateway-key: skipped"
fi

# --- 4) providers ------------------------------------------------------------
if [ "${GUDA_SEED_SKIP_PROVIDERS:-0}" != "1" ]; then
  if [ ! -f "$SEED_PROVIDERS" ]; then
    log "providers: seed-provider-keys.sh not found at $SEED_PROVIDERS"
    exit 1
  fi
  # Need at least one provider secret to do useful work; otherwise soft-skip.
  if [ -z "${GROK_1_API_KEY:-${GROK_API_KEY:-}}" ] \
    && [ -z "${TAVILY_API_KEYS:-${TAVILY_1_API_KEY:-${TAVILY_API_KEY:-}}}" ] \
    && [ -z "${FIRECRAWL_1_API_KEY:-${FIRECRAWL_API_KEY:-}}" ]; then
    log "providers: no GROK/TAVILY/FIRECRAWL keys in env — skip (export secrets then re-run)"
  else
    log "providers: seed-provider-keys.sh"
    bash "$SEED_PROVIDERS" "$ADM"
  fi
else
  log "providers: skipped"
fi

log "done"
log "verify: $ADM --db \$DB_PATH --master-key \$GUDA_MASTER_KEY_PATH provider-endpoint list"
log "admin:  Coolify Environment GUDA_ADMIN_TOKEN and/or $SAVE_ENV"
