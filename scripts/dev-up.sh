#!/usr/bin/env bash
# Boot the local guda-gateway required for dev (persistent macOS paths).
# Usage:
#   scripts/dev-up.sh          # start if not already healthy
#   scripts/dev-up.sh --rebuild
#   scripts/dev-up.sh --fg     # foreground (no nohup)
#   scripts/dev-up.sh --status
#   scripts/dev-up.sh --stop

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SECRETS="${GUDA_SECRETS_ENV:-$HOME/.secrets/guda-gateway.env}"
LOG="${GUDA_DEV_LOG:-/tmp/guda-gateway-dev.log}"
PID_FILE="${GUDA_DEV_PID_FILE:-/tmp/guda-gateway-dev.pid}"
ADDR_DEFAULT="127.0.0.1:8080"

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
}

health_url() {
  local hostport="${ADDR#*//}"
  hostport="${hostport%%/*}"
  echo "http://${hostport}/healthz"
}

is_healthy() {
  curl -fsS --max-time 1 "$(health_url)" >/dev/null 2>&1
}

print_status() {
  load_env
  echo "ADDR=$ADDR"
  echo "DB_PATH=$DB_PATH"
  echo "GUDA_MASTER_KEY_PATH=$GUDA_MASTER_KEY_PATH"
  echo "LOG=$LOG"
  if is_healthy; then
    echo "health: ok ($(health_url))"
  else
    echo "health: down"
  fi
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid="$(cat "$PID_FILE" 2>/dev/null || true)"
    if [[ -n "${pid:-}" ]] && kill -0 "$pid" 2>/dev/null; then
      echo "pid: $pid (from $PID_FILE)"
    else
      echo "pid file stale: $PID_FILE"
    fi
  fi
  pgrep -lf '[g]uda-gateway$' 2>/dev/null || pgrep -lf 'guda-gateway' 2>/dev/null || true
}

stop_gateway() {
  if [[ -f "$PID_FILE" ]]; then
    local pid
    pid="$(cat "$PID_FILE" 2>/dev/null || true)"
    if [[ -n "${pid:-}" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      for _ in 1 2 3 4 5 6 7 8 9 10; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.2
      done
      if kill -0 "$pid" 2>/dev/null; then
        kill -9 "$pid" 2>/dev/null || true
      fi
    fi
    rm -f "$PID_FILE"
  fi
  # Best-effort: stop listeners on the configured port only if they look like guda-gateway.
  load_env
  local port="${ADDR##*:}"
  if command -v lsof >/dev/null 2>&1; then
    local pids
    pids="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null || true)"
    if [[ -n "$pids" ]]; then
      while read -r p; do
        [[ -z "$p" ]] && continue
        if ps -p "$p" -o args= 2>/dev/null | grep -q 'guda-gateway'; then
          kill "$p" 2>/dev/null || true
        fi
      done <<<"$pids"
    fi
  fi
}

ensure_dirs() {
  mkdir -p "$(dirname "$DB_PATH")" "$(dirname "$GUDA_MASTER_KEY_PATH")"
}

ensure_binary() {
  local bin="$ROOT/guda-gateway"
  if [[ "${REBUILD:-0}" == "1" ]] || [[ ! -x "$bin" ]]; then
    echo "dev-up: building guda-gateway..."
    (cd "$ROOT" && CGO_ENABLED=0 go build -o guda-gateway ./cmd/guda-gateway)
  fi
  [[ -x "$bin" ]] || die "missing binary $bin"
}

start_gateway() {
  load_env
  ensure_dirs
  ensure_binary

  if is_healthy; then
    echo "dev-up: already healthy at $(health_url)"
    print_status
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
      echo "dev-up: up — $(health_url)"
      echo "dev-up: log $LOG  pid $(cat "$PID_FILE")"
      return 0
    fi
    sleep 0.2
  done
  echo "dev-up: started but health check failed; last log lines:" >&2
  tail -n 40 "$LOG" 2>/dev/null || true
  exit 1
}

REBUILD=0
FOREGROUND=0
cmd="start"
for arg in "$@"; do
  case "$arg" in
    --rebuild) REBUILD=1 ;;
    --fg|--foreground) FOREGROUND=1 ;;
    --status) cmd="status" ;;
    --stop) cmd="stop" ;;
    -h|--help)
      sed -n '2,10p' "$0"
      exit 0
      ;;
    *) die "unknown arg: $arg" ;;
  esac
done

case "$cmd" in
  status) print_status ;;
  stop) stop_gateway; echo "dev-up: stopped" ;;
  start) start_gateway ;;
esac
