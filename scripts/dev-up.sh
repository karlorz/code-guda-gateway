#!/usr/bin/env bash
# Boot local guda-gateway for dev (persistent macOS paths), optionally with Vite HMR.
#
# Usage:
#   scripts/dev-up.sh              # start API if not already healthy
#   scripts/dev-up.sh --ui         # API + Vite admin HMR (preferred for web/admin work)
#   scripts/dev-up.sh --ui-only    # Vite only (API must already be up)
#   scripts/dev-up.sh --rebuild    # rebuild Go binary then start
#   scripts/dev-up.sh --fg         # gateway in foreground (no nohup; ignores --ui)
#   scripts/dev-up.sh --status
#   scripts/dev-up.sh --stop       # stop gateway and Vite (if started by dev-up)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SECRETS="${GUDA_SECRETS_ENV:-$HOME/.secrets/guda-gateway.env}"
LOG="${GUDA_DEV_LOG:-/tmp/guda-gateway-dev.log}"
PID_FILE="${GUDA_DEV_PID_FILE:-/tmp/guda-gateway-dev.pid}"
VITE_LOG="${GUDA_VITE_LOG:-/tmp/guda-gateway-vite.log}"
VITE_PID_FILE="${GUDA_VITE_PID_FILE:-/tmp/guda-gateway-vite.pid}"
ADDR_DEFAULT="127.0.0.1:8080"
VITE_HOST="${GUDA_VITE_HOST:-127.0.0.1}"
VITE_PORT="${GUDA_VITE_PORT:-5173}"
ADMIN_SRC="$ROOT/web/admin"

die() { echo "dev-up: $*" >&2; exit 1; }

load_env() {
  if [[ -f "$SECRETS" ]]; then
    # shellcheck disable=SC1090
    set -a
    # Expand unquoted $HOME-style paths from secrets if present after source.
    source "$SECRETS"
    set +a
  else
    echo "dev-up: warning: secrets file missing ($SECRETS); using persistent path defaults" >&2
  fi

  export ADDR="${ADDR:-$ADDR_DEFAULT}"
  # Prefer explicit persistent pair when secrets only had placeholders.
  if [[ -z "${DB_PATH:-}" || "$DB_PATH" == *'$HOME'* ]]; then
    export DB_PATH="${HOME}/.local/share/guda-gateway/gateway.db"
  fi
  if [[ -z "${GUDA_MASTER_KEY_PATH:-}" || "$GUDA_MASTER_KEY_PATH" == *'$HOME'* ]]; then
    export GUDA_MASTER_KEY_PATH="${HOME}/.local/share/guda-gateway/master.key"
  fi
  # Local plain-HTTP admin UI.
  export GUDA_ADMIN_COOKIE_SECURE="${GUDA_ADMIN_COOKIE_SECURE:-false}"
  # Vite proxies /admin/api here (same host:port the gateway listens on).
  export GUDA_DEV_API="${GUDA_DEV_API:-http://${ADDR#*//}}"
  # If ADDR is host:port without scheme:
  if [[ "$GUDA_DEV_API" != http://* && "$GUDA_DEV_API" != https://* ]]; then
    export GUDA_DEV_API="http://${ADDR}"
  fi
}

health_url() {
  local hostport="${ADDR#*//}"
  hostport="${hostport%%/*}"
  echo "http://${hostport}/healthz"
}

vite_admin_url() {
  echo "http://${VITE_HOST}:${VITE_PORT}/admin/"
}

is_healthy() {
  curl -fsS --max-time 1 "$(health_url)" >/dev/null 2>&1
}

is_vite_up() {
  # Vite serves the SPA under base /admin/; any 2xx/3xx/4xx from the server means up.
  curl -fsS --max-time 1 -o /dev/null -w '%{http_code}' "$(vite_admin_url)" 2>/dev/null | grep -qE '^[234]'
}

pid_alive() {
  local f="$1"
  [[ -f "$f" ]] || return 1
  local pid
  pid="$(cat "$f" 2>/dev/null || true)"
  [[ -n "${pid:-}" ]] && kill -0 "$pid" 2>/dev/null
}

print_status() {
  load_env
  echo "ADDR=$ADDR"
  echo "DB_PATH=$DB_PATH"
  echo "GUDA_MASTER_KEY_PATH=$GUDA_MASTER_KEY_PATH"
  echo "GUDA_DEV_API=$GUDA_DEV_API"
  echo "LOG=$LOG"
  echo "VITE_LOG=$VITE_LOG"
  if is_healthy; then
    echo "gateway health: ok ($(health_url))"
  else
    echo "gateway health: down"
  fi
  if pid_alive "$PID_FILE"; then
    echo "gateway pid: $(cat "$PID_FILE") (from $PID_FILE)"
  elif [[ -f "$PID_FILE" ]]; then
    echo "gateway pid file stale: $PID_FILE"
  fi
  if is_vite_up; then
    echo "vite admin: ok ($(vite_admin_url))"
  else
    echo "vite admin: down"
  fi
  if pid_alive "$VITE_PID_FILE"; then
    echo "vite pid: $(cat "$VITE_PID_FILE") (from $VITE_PID_FILE)"
  elif [[ -f "$VITE_PID_FILE" ]]; then
    echo "vite pid file stale: $VITE_PID_FILE"
  fi
  if is_healthy && is_vite_up; then
    echo "hint: use $(vite_admin_url) for HMR; http://${ADDR}/admin/ is the embedded SPA snapshot"
  elif is_healthy; then
    echo "hint: API only — run: $0 --ui   (or: bun run --cwd web/admin dev)"
  fi
  pgrep -lf '[g]uda-gateway$' 2>/dev/null || pgrep -lf 'guda-gateway' 2>/dev/null || true
}

stop_pidfile() {
  local f="$1"
  local label="$2"
  if [[ -f "$f" ]]; then
    local pid
    pid="$(cat "$f" 2>/dev/null || true)"
    if [[ -n "${pid:-}" ]] && kill -0 "$pid" 2>/dev/null; then
      # Prefer process-group kill when start used setsid (session leader == pid).
      kill -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
      for _ in 1 2 3 4 5 6 7 8 9 10; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.2
      done
      if kill -0 "$pid" 2>/dev/null; then
        kill -9 -- "-$pid" 2>/dev/null || kill -9 "$pid" 2>/dev/null || true
      fi
      echo "dev-up: stopped $label pid $pid"
    fi
    rm -f "$f"
  fi
}

# Best-effort: kill listeners on port whose args match pattern (e.g. guda-gateway, vite).
stop_listener_if() {
  local port="$1"
  local pattern="$2"
  command -v lsof >/dev/null 2>&1 || return 0
  local pids
  pids="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null || true)"
  [[ -z "$pids" ]] && return 0
  while read -r p; do
    [[ -z "$p" ]] && continue
    if ps -p "$p" -o args= 2>/dev/null | grep -Eq "$pattern"; then
      kill "$p" 2>/dev/null || true
    fi
  done <<<"$pids"
}

stop_gateway() {
  stop_pidfile "$PID_FILE" "gateway"
  load_env
  local port="${ADDR##*:}"
  stop_listener_if "$port" 'guda-gateway'
}

stop_vite() {
  stop_pidfile "$VITE_PID_FILE" "vite"
  stop_listener_if "$VITE_PORT" 'vite|web/admin'
}


ensure_dirs() {
  mkdir -p "$(dirname "$DB_PATH")" "$(dirname "$GUDA_MASTER_KEY_PATH")"
}

ensure_binary() {
  local bin="$ROOT/guda-gateway"
  if [[ "${REBUILD:-0}" == "1" ]] || [[ ! -x "$bin" ]]; then
    echo "dev-up: building guda-gateway..."
    (cd "$ROOT" && CGO_ENABLED=0 go build -buildvcs=false -o guda-gateway ./cmd/guda-gateway)
  fi
  [[ -x "$bin" ]] || die "missing binary $bin"
}

ensure_admin_deps() {
  command -v bun >/dev/null 2>&1 || die "bun is required for --ui (https://bun.sh)"
  [[ -f "$ADMIN_SRC/package.json" ]] || die "missing $ADMIN_SRC/package.json"
  if [[ ! -x "$ADMIN_SRC/node_modules/.bin/vite" ]]; then
    echo "dev-up: installing web/admin deps (bun install)..."
    (cd "$ADMIN_SRC" && bun install --frozen-lockfile)
  fi
  [[ -x "$ADMIN_SRC/node_modules/.bin/vite" ]] || die "vite missing after bun install"
}

start_gateway() {
  load_env
  ensure_dirs
  ensure_binary

  if is_healthy; then
    echo "dev-up: gateway already healthy at $(health_url)"
    return 0
  fi

  local bin="$ROOT/guda-gateway"
  echo "dev-up: starting $bin (ADDR=$ADDR DB_PATH=$DB_PATH)"
  if [[ "${FOREGROUND:-0}" == "1" ]]; then
    exec env \
      ADDR="$ADDR" \
      DB_PATH="$DB_PATH" \
      GUDA_MASTER_KEY_PATH="$GUDA_MASTER_KEY_PATH" \
      GUDA_ADMIN_COOKIE_SECURE="$GUDA_ADMIN_COOKIE_SECURE" \
      "$bin"
  fi

  nohup env \
    ADDR="$ADDR" \
    DB_PATH="$DB_PATH" \
    GUDA_MASTER_KEY_PATH="$GUDA_MASTER_KEY_PATH" \
    GUDA_ADMIN_COOKIE_SECURE="$GUDA_ADMIN_COOKIE_SECURE" \
    "$bin" >"$LOG" 2>&1 &
  echo $! >"$PID_FILE"

  for _ in $(seq 1 30); do
    if is_healthy; then
      echo "dev-up: gateway up — $(health_url)"
      echo "dev-up: log $LOG  pid $(cat "$PID_FILE")"
      return 0
    fi
    sleep 0.2
  done
  echo "dev-up: started but health check failed; last log lines:" >&2
  tail -n 40 "$LOG" 2>/dev/null || true
  exit 1
}

start_vite() {
  load_env
  ensure_admin_deps

  if is_vite_up; then
    echo "dev-up: vite already up at $(vite_admin_url)"
    return 0
  fi

  if ! is_healthy; then
    die "gateway not healthy at $(health_url); start API first (omit --ui-only) or check --status"
  fi

  echo "dev-up: starting Vite HMR (proxy /admin/api → $GUDA_DEV_API)"
  # Start under setsid when available so --stop can kill the process group;
  # on macOS setsid is often missing — fall back to plain bg + lsof cleanup.
  if command -v setsid >/dev/null 2>&1; then
    setsid env \
      GUDA_DEV_API="$GUDA_DEV_API" \
      bash -c "cd \"$ADMIN_SRC\" && exec bun run dev -- --port \"$VITE_PORT\" --strictPort" \
      >"$VITE_LOG" 2>&1 &
  else
    (
      cd "$ADMIN_SRC"
      exec env \
        GUDA_DEV_API="$GUDA_DEV_API" \
        bun run dev -- --port "$VITE_PORT" --strictPort
    ) >"$VITE_LOG" 2>&1 &
  fi
  echo $! >"$VITE_PID_FILE"


  for _ in $(seq 1 50); do
    if is_vite_up; then
      echo "dev-up: vite up — $(vite_admin_url)"
      echo "dev-up: open that URL for hot reload (not :8080/admin which is the embedded build)"
      echo "dev-up: vite log $VITE_LOG  pid $(cat "$VITE_PID_FILE")"
      return 0
    fi
    sleep 0.2
  done
  echo "dev-up: vite started but admin URL not responding; last log lines:" >&2
  tail -n 40 "$VITE_LOG" 2>/dev/null || true
  exit 1
}

REBUILD=0
FOREGROUND=0
WITH_UI=0
UI_ONLY=0
cmd="start"
for arg in "$@"; do
  case "$arg" in
    --rebuild) REBUILD=1 ;;
    --fg|--foreground) FOREGROUND=1 ;;
    --ui|--with-ui) WITH_UI=1 ;;
    --ui-only) UI_ONLY=1; WITH_UI=1 ;;
    --status) cmd="status" ;;
    --stop) cmd="stop" ;;
    -h|--help)
      sed -n '2,14p' "$0"
      exit 0
      ;;
    *) die "unknown arg: $arg" ;;
  esac
done

if [[ "$FOREGROUND" == "1" && "$WITH_UI" == "1" ]]; then
  die "--fg cannot be combined with --ui (run gateway in bg, or start vite separately)"
fi

case "$cmd" in
  status) print_status ;;
  stop)
    stop_vite
    stop_gateway
    echo "dev-up: stopped"
    ;;
  start)
    if [[ "$UI_ONLY" == "1" ]]; then
      start_vite
    else
      start_gateway
      if [[ "$WITH_UI" == "1" ]]; then
        start_vite
      else
        load_env
        echo "dev-up: API-only. For admin HMR: $0 --ui   → $(vite_admin_url)"
      fi
    fi
    ;;
esac
