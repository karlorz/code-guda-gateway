#!/usr/bin/env bash
# Seed provider keys into the guda-gateway SQLite DB from env vars.
# Bash-only (no zsh `read -a`). Reads secrets from the env file sourced
# by the caller; never echoes keys to stdout.
#
# Usage:
#   set -a; . ~/.secrets/guda-gateway.env; set +a
#   export DB_PATH=... GUDA_MASTER_KEY_PATH=...
#   ./scripts/seed-provider-keys.sh [guda-gateway-admin-path]
#
# Idempotent per (provider, name): re-runs skip when that name already
# exists (ErrDuplicateName). Use distinct names (e.g. tavily-1, tavily-2)
# for multiple keys; same raw key under a new name still inserts a row.
set -euo pipefail

ADM="${1:-./guda-gateway-admin}"
DB="${DB_PATH:?DB_PATH must be set}"
MK="${GUDA_MASTER_KEY_PATH:?GUDA_MASTER_KEY_PATH must be set}"

add_key() {
  local provider="$1" name="$2" key="$3" out rc=0
  [ -z "$key" ] && { echo "skip $provider/$name: empty key" >&2; return 0; }
  out=$(printf '%s' "$key" | "$ADM" --db "$DB" --master-key "$MK" \
    provider-key add --provider "$provider" --name "$name" 2>&1) || rc=$?
  if [ "$rc" -ne 0 ]; then
    if printf '%s' "$out" | grep -q 'name already exists'; then
      echo "skip $provider/$name: already exists" >&2
      return 0
    fi
    printf '%s\n' "$out" >&2
    return "$rc"
  fi
  printf '%s\n' "$out"
}

# Grok inference key (grok2api app.api_key)
add_key grok grok2api "${GROK_API_KEY:-}"

# Firecrawl key
add_key firecrawl gh01 "${FIRECRAWL_API_KEY:-}"

# Tavily keys: comma-separated list -> one row per key
if [ -n "${TAVILY_API_KEYS:-}" ]; then
  i=1
  while IFS= read -r k; do
    k="${k//\"/}"; k="${k// /}"
    [ -z "$k" ] && continue
    add_key tavily "tavily-$i" "$k"
    i=$((i+1))
  done < <(printf '%s\n' "$TAVILY_API_KEYS" | tr ',' '\n')
fi

echo "--- provider-key list ---"
"$ADM" --db "$DB" --master-key "$MK" provider-key list
