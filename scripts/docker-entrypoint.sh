#!/usr/bin/env bash
# Container entrypoint: Coolify-friendly admin bootstrap (approach C).
#
# Prefer GUDA_ADMIN_TOKEN from Coolify magic env:
#   GUDA_ADMIN_TOKEN=${SERVICE_PASSWORD_64_GUDAADMIN}
# On first boot (or when syncing), hash that value into SQLite and mirror to
# the data volume (admin-credentials.env + ADMIN_LOGIN.txt).
#
# SQLite stores only a hash. Coolify Environment UI shows the magic password.
#
# Optional full seed (same coverage as VM/kr01 seed-instance.sh):
#   GUDA_SEED_ON_START=1  plus provider secrets in env → after admin bootstrap,
#   runs seed-instance (gateway key + provider endpoints). Default off so
#   Coolify stays lean unless you opt in.
set -euo pipefail

DB_PATH="${DB_PATH:-/var/lib/code-guda-gateway/gateway.db}"
GUDA_MASTER_KEY_PATH="${GUDA_MASTER_KEY_PATH:-/etc/code-guda-gateway/master.key}"
CRED_DIR="$(dirname "$DB_PATH")"
CRED_ENV="${GUDA_ADMIN_CREDENTIALS_PATH:-$CRED_DIR/admin-credentials.env}"
CRED_NOTE="${GUDA_ADMIN_NOTE_PATH:-$CRED_DIR/ADMIN_LOGIN.txt}"
BOOTSTRAP="${GUDA_BOOTSTRAP_ADMIN_TOKEN:-1}"
SEED_ON_START="${GUDA_SEED_ON_START:-0}"

write_login_note() {
  local mode="$1"
  umask 077
  cat >"$CRED_NOTE" <<EOF
code-guda-gateway admin login (${mode})

Admin UI:  /admin  (Coolify HTTPS URL)
Primary:   Coolify → Environment → GUDA_ADMIN_TOKEN
Mirror:    ${CRED_ENV}

SQLite stores a hash only. Use Coolify Environment for UI-visible login.
Volume file is an SSH/storage mirror (grok2api-style).

If env and DB diverge after a Coolify password regenerate:

  guda-gateway-admin token sync-env --save-env ${CRED_ENV}

EOF
  chmod 600 "$CRED_NOTE" 2>/dev/null || true
}

if [[ "$BOOTSTRAP" == "1" || "$BOOTSTRAP" == "true" ]]; then
  mkdir -p "$CRED_DIR" "$(dirname "$GUDA_MASTER_KEY_PATH")"

  if [[ -n "${GUDA_ADMIN_TOKEN:-}" ]]; then
    # Align DB hash to Coolify/env secret (init or replace). Always mirror to volume.
    if guda-gateway-admin \
      --db "$DB_PATH" \
      --master-key "$GUDA_MASTER_KEY_PATH" \
      token sync-env --save-env "$CRED_ENV" >/dev/null 2>&1; then
      write_login_note "Coolify/env GUDA_ADMIN_TOKEN"
      echo "guda-gateway: admin token synced from env; mirrored to $CRED_ENV" >&2
    else
      echo "guda-gateway: token sync-env failed (check GUDA_ADMIN_TOKEN length/format)" >&2
    fi
  else
    # No Coolify password: generate classic gat_… once if DB empty.
    if guda-gateway-admin \
      --db "$DB_PATH" \
      --master-key "$GUDA_MASTER_KEY_PATH" \
      token init --save-env "$CRED_ENV" >/dev/null 2>&1; then
      write_login_note "generated gat_ token (set Coolify SERVICE_PASSWORD for UI)"
      echo "guda-gateway: admin token generated; written to $CRED_ENV" >&2
    elif [[ -f "$CRED_ENV" ]]; then
      :
    elif [[ ! -f "$CRED_NOTE" ]]; then
      cat >"$CRED_NOTE" <<EOF
code-guda-gateway admin login

No GUDA_ADMIN_TOKEN in environment and token may already exist in DB.
Set Coolify magic env GUDA_ADMIN_TOKEN=\${SERVICE_PASSWORD_64_GUDAADMIN}
then redeploy, or:

  guda-gateway-admin token rotate --save-env $CRED_ENV
EOF
      chmod 600 "$CRED_NOTE" 2>/dev/null || true
      echo "guda-gateway: see $CRED_NOTE" >&2
    fi
  fi
fi

if [[ "$SEED_ON_START" == "1" || "$SEED_ON_START" == "true" ]]; then
  export GUDA_SEED_SAVE_ENV="${GUDA_SEED_SAVE_ENV:-$CRED_ENV}"
  export GUDA_SEED_PROFILE="${GUDA_SEED_PROFILE:-coolify}"
  # Admin already handled above; avoid double sync noise.
  export GUDA_SEED_SKIP_ADMIN="${GUDA_SEED_SKIP_ADMIN:-1}"
  if command -v seed-instance >/dev/null 2>&1; then
    echo "guda-gateway: GUDA_SEED_ON_START=1 → seed-instance" >&2
    seed-instance || echo "guda-gateway: seed-instance failed (gateway still starting)" >&2
  else
    echo "guda-gateway: seed-instance not in PATH" >&2
  fi
fi

exec guda-gateway "$@"
