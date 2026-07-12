#!/usr/bin/env bash
# Container entrypoint: ensure admin token exists for Coolify/first-boot UX.
# Raw token is written once to a volume file (like grok2api's on-disk config).
# The DB only stores a hash — the raw token cannot be recovered later.
set -euo pipefail

DB_PATH="${DB_PATH:-/var/lib/code-guda-gateway/gateway.db}"
GUDA_MASTER_KEY_PATH="${GUDA_MASTER_KEY_PATH:-/etc/code-guda-gateway/master.key}"
CRED_DIR="$(dirname "$DB_PATH")"
CRED_ENV="${GUDA_ADMIN_CREDENTIALS_PATH:-$CRED_DIR/admin-credentials.env}"
CRED_NOTE="${GUDA_ADMIN_NOTE_PATH:-$CRED_DIR/ADMIN_LOGIN.txt}"
BOOTSTRAP="${GUDA_BOOTSTRAP_ADMIN_TOKEN:-1}"

if [[ "$BOOTSTRAP" == "1" || "$BOOTSTRAP" == "true" ]]; then
  mkdir -p "$CRED_DIR" "$(dirname "$GUDA_MASTER_KEY_PATH")"
  # token init fails if already set (exit non-zero) — that is expected after first boot.
  if raw="$(
    guda-gateway-admin \
      --db "$DB_PATH" \
      --master-key "$GUDA_MASTER_KEY_PATH" \
      token init \
      --save-env "$CRED_ENV" 2>/dev/null
  )"; then
    raw="$(printf '%s\n' "$raw" | tr -d '\r' | head -n1)"
    umask 077
    cat >"$CRED_NOTE" <<EOF
code-guda-gateway admin login (generated on first boot)

Admin UI:  /admin  (use the public Coolify HTTPS URL)
Token:     see admin-credentials.env (GUDA_ADMIN_TOKEN)
File:      $CRED_ENV

This token is shown only at init/rotate. The database stores a hash only.
To rotate (invalidates the old token and rewrites the files):

  guda-gateway-admin token rotate --save-env $CRED_ENV

EOF
    chmod 600 "$CRED_ENV" "$CRED_NOTE" 2>/dev/null || true
    # Log path only — never log the raw token.
    echo "guda-gateway: first-boot admin token written to $CRED_ENV (see $CRED_NOTE)" >&2
  elif [[ -f "$CRED_ENV" ]]; then
    : # already bootstrapped
  else
    # Already initialized in DB but credentials file missing (e.g. volume wipe of env only).
    # Operator must rotate to obtain a new raw token.
    if [[ ! -f "$CRED_NOTE" ]]; then
      cat >"$CRED_NOTE" <<EOF
code-guda-gateway admin login

No raw token file found, but the database may already have an admin token hash.
The raw token cannot be recovered from the database.

Rotate to mint a new token and write admin-credentials.env:

  guda-gateway-admin token rotate --save-env $CRED_ENV

Then open /admin and paste GUDA_ADMIN_TOKEN from that file.
EOF
      chmod 600 "$CRED_NOTE" 2>/dev/null || true
      echo "guda-gateway: admin token already set; rotate required (see $CRED_NOTE)" >&2
    fi
  fi
fi

exec guda-gateway "$@"
