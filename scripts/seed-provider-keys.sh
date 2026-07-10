#!/usr/bin/env bash
# Seed provider endpoint pairs into the guda-gateway SQLite DB from env vars.
# Bash-only (no zsh `read -a`). Reads secrets from the env file sourced
# by the caller; never echoes keys to stdout or places them on argv.
#
# Usage:
#   set -a; . ~/.secrets/guda-gateway.env; set +a
#   export DB_PATH=... GUDA_MASTER_KEY_PATH=...
#   ./scripts/seed-provider-keys.sh [guda-gateway-admin-path]
#
# Each row is an atomic (base_url, encrypted_key) pair created via
# `provider-endpoint add --base-url ...` with the key on stdin.
# Base URL per provider: $GROK_BASE_URL / $TAVILY_BASE_URL /
# $FIRECRAWL_BASE_URL, else the compiled provider default.
#
# Idempotent per (provider, name): re-runs skip when that name already
# exists (ErrDuplicateName). Use distinct names (e.g. tavily-1, tavily-2)
# for multiple keys; same raw key under a new name still inserts a row.
set -euo pipefail

ADM="${1:-./guda-gateway-admin}"
DB="${DB_PATH:?DB_PATH must be set}"
MK="${GUDA_MASTER_KEY_PATH:?GUDA_MASTER_KEY_PATH must be set}"

# Compiled defaults (must match internal/providers Default*BaseURL).
DEFAULT_GROK_BASE_URL="https://api.x.ai/v1"
DEFAULT_TAVILY_BASE_URL="https://api.tavily.com"
DEFAULT_FIRECRAWL_BASE_URL="https://api.firecrawl.dev/v2"

base_url_for() {
  local provider="$1"
  case "$provider" in
    grok)
      printf '%s' "${GROK_BASE_URL:-$DEFAULT_GROK_BASE_URL}"
      ;;
    tavily)
      printf '%s' "${TAVILY_BASE_URL:-$DEFAULT_TAVILY_BASE_URL}"
      ;;
    firecrawl)
      printf '%s' "${FIRECRAWL_BASE_URL:-$DEFAULT_FIRECRAWL_BASE_URL}"
      ;;
    *)
      echo "unknown provider: $provider" >&2
      return 1
      ;;
  esac
}

add_endpoint() {
  local provider="$1" name="$2" key="$3" base_url out rc=0
  [ -z "$key" ] && { echo "skip $provider/$name: empty key" >&2; return 0; }
  base_url="$(base_url_for "$provider")"
  out=$(printf '%s' "$key" | "$ADM" --db "$DB" --master-key "$MK" \
    provider-endpoint add \
    --provider "$provider" \
    --name "$name" \
    --base-url "$base_url" 2>&1) || rc=$?
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
add_endpoint grok grok2api "${GROK_API_KEY:-}"

# Firecrawl key
add_endpoint firecrawl gh01 "${FIRECRAWL_API_KEY:-}"

# Tavily keys: comma-separated list -> one row per key
if [ -n "${TAVILY_API_KEYS:-}" ]; then
  i=1
  while IFS= read -r k; do
    k="${k//\"/}"; k="${k// /}"
    [ -z "$k" ] && continue
    add_endpoint tavily "tavily-$i" "$k"
    i=$((i+1))
  done < <(printf '%s\n' "$TAVILY_API_KEYS" | tr ',' '\n')
fi

echo "--- provider-endpoint list ---"
"$ADM" --db "$DB" --master-key "$MK" provider-endpoint list
