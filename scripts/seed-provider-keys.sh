#!/usr/bin/env bash
# Seed provider endpoint pairs + quota sidecars into the guda-gateway SQLite DB.
# Bash-only (no zsh arrays). Never echoes raw keys or places them on argv.
#
# Usage:
#   set -a; . ~/.secrets/guda-gateway.env; set +a
#   export DB_PATH=... GUDA_MASTER_KEY_PATH=...
#   ./scripts/seed-provider-keys.sh [guda-gateway-admin-path]
#
# Canonical topology:
#   grok-1       inference → GROK_1_BASE_URL (default https://new.karldigi.dev/v1)
#                quota     → disabled; New API owns channel health and quota
#   tavily-1..N     inference → TAVILY_BASE_URL (official); quota endpoint_credentials
#   firecrawl-1..N  inference → FIRECRAWL_BASE_URL (official); quota endpoint_credentials
#
# Direct Grok/Grok2API deployments remain supported through the generic
# provider-endpoint CLI/UI, but are not part of the canonical seed.
#
# Idempotent per (provider, name): skips when name already exists.
# Existing rows can still get quota via set-quota + rotate-quota-key after seed.
set -euo pipefail

ADM="${1:-./guda-gateway-admin}"
DB="${DB_PATH:?DB_PATH must be set}"
MK="${GUDA_MASTER_KEY_PATH:?GUDA_MASTER_KEY_PATH must be set}"

DEFAULT_GROK_BASE_URL="https://new.karldigi.dev/v1"
DEFAULT_TAVILY_BASE_URL="https://api.tavily.com"
DEFAULT_FIRECRAWL_BASE_URL="https://api.firecrawl.dev/v2"

run_adm() {
  "$ADM" --db "$DB" --master-key "$MK" "$@"
}

# Look up endpoint id by provider+name (ignore archived). Prefer sqlite3.
endpoint_id() {
  local provider="$1" name="$2" id=""
  if command -v sqlite3 >/dev/null 2>&1; then
    id="$(sqlite3 "$DB" "SELECT id FROM provider_keys WHERE provider='${provider//\'/\'\'}' AND name='${name//\'/\'\'}' AND (archived_at IS NULL OR archived_at='') ORDER BY id LIMIT 1;")"
  fi
  if [ -z "$id" ]; then
    id="$(run_adm provider-endpoint list 2>/dev/null | awk -v p="$provider" -v n="$name" '
      NR>1 && $2==p && $3==n { print $1; exit }
    ')"
  fi
  printf '%s' "$id"
}

endpoint_quota_meta() {
  # Prints: mode<TAB>flow
  local provider="$1" name="$2"
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 -separator $'\t' "$DB" \
      "SELECT COALESCE(quota_mode,''), COALESCE(quota_flow,'')
       FROM provider_keys
       WHERE provider='${provider//\'/\'\'}' AND name='${name//\'/\'\'}'
         AND (archived_at IS NULL OR archived_at='')
       ORDER BY id LIMIT 1;"
    return 0
  fi
  printf '\t'
}

add_endpoint() {
  # add_endpoint provider name base_url raw_key [quota_mode] [quota_flow]
  local provider="$1" name="$2" base_url="$3" key="$4"
  local quota_mode="${5:-}" quota_flow="${6:-}"
  local out rc=0 args

  [ -z "$key" ] && { echo "skip $provider/$name: empty inference key" >&2; return 0; }

  args=(provider-endpoint add --provider "$provider" --name "$name" --base-url "$base_url")
  if [ -n "$quota_mode" ]; then
    args+=(--quota-mode "$quota_mode")
  fi
  if [ -n "$quota_flow" ]; then
    args+=(--quota-flow "$quota_flow")
  fi
  out=$(printf '%s' "$key" | run_adm "${args[@]}" 2>&1) || rc=$?

  if [ "$rc" -ne 0 ]; then
    if printf '%s' "$out" | grep -q 'name already exists'; then
      echo "skip $provider/$name: already exists" >&2
      if [ "$quota_mode" = "endpoint_credentials" ] && [ -n "$quota_flow" ]; then
        ensure_shared_quota "$provider" "$name" "$quota_flow"
      fi
      return 0
    fi
    printf '%s\n' "$out" >&2
    return "$rc"
  fi
  printf '%s\n' "$out"
}

ensure_shared_quota() {
  local provider="$1" name="$2" flow="$3"
  local id mode current_flow meta

  id="$(endpoint_id "$provider" "$name")"
  [ -z "$id" ] && return 0
  meta="$(endpoint_quota_meta "$provider" "$name")"
  mode="$(printf '%s' "$meta" | cut -f1)"
  current_flow="$(printf '%s' "$meta" | cut -f2)"
  if [ "$mode" != "endpoint_credentials" ] || [ "$current_flow" != "$flow" ]; then
    if run_adm provider-endpoint set-quota \
      --id "$id" \
      --mode endpoint_credentials \
      --flow "$flow" >/dev/null; then
      echo "set-quota $provider/$name id=$id endpoint_credentials" >&2
    fi
  fi
}

# --- Grok-1 ------------------------------------------------------------------
GROK_NAME="${GROK_1_NAME:-grok-1}"
GROK_BASE="${GROK_1_BASE_URL:-${GROK_BASE_URL:-$DEFAULT_GROK_BASE_URL}}"
GROK_KEY="${GROK_1_API_KEY:-${GROK_API_KEY:-}}"

add_endpoint grok "$GROK_NAME" "$GROK_BASE" "$GROK_KEY" disabled

# Shared-credential providers (Tavily / Firecrawl) use the inference endpoint
# pair itself for quota refresh.
seed_shared_endpoint() {
  local provider="$1" base="$2" flow="$3" name="$4" key="$5"
  [ -z "$key" ] && return 0
  add_endpoint "$provider" "$name" "$base" "$key" endpoint_credentials "$flow"
}

seed_shared_csv() {
  local provider="$1" base="$2" flow="$3" prefix="$4" keys="$5"
  local i=1 k
  while IFS= read -r k; do
    k="${k//\"/}"; k="${k// /}"
    [ -z "$k" ] && continue
    seed_shared_endpoint "$provider" "$base" "$flow" "$prefix-$i" "$k"
    i=$((i + 1))
  done < <(printf '%s\n' "$keys" | tr ',' '\n')
}

# --- Firecrawl 1..N ----------------------------------------------------------
FC_BASE="${FIRECRAWL_BASE_URL:-$DEFAULT_FIRECRAWL_BASE_URL}"

if [ -n "${FIRECRAWL_API_KEYS:-}" ]; then
  seed_shared_csv firecrawl "$FC_BASE" firecrawl_credit_usage firecrawl "$FIRECRAWL_API_KEYS"
elif [ -n "${FIRECRAWL_1_API_KEY:-}" ] || [ -n "${FIRECRAWL_2_API_KEY:-}" ] || [ -n "${FIRECRAWL_3_API_KEY:-}" ]; then
  seed_shared_endpoint firecrawl "$FC_BASE" firecrawl_credit_usage "${FIRECRAWL_1_NAME:-firecrawl-1}" "${FIRECRAWL_1_API_KEY:-}"
  seed_shared_endpoint firecrawl "$FC_BASE" firecrawl_credit_usage "${FIRECRAWL_2_NAME:-firecrawl-2}" "${FIRECRAWL_2_API_KEY:-}"
  seed_shared_endpoint firecrawl "$FC_BASE" firecrawl_credit_usage "${FIRECRAWL_3_NAME:-firecrawl-3}" "${FIRECRAWL_3_API_KEY:-}"
elif [ -n "${FIRECRAWL_API_KEY:-}" ]; then
  seed_shared_endpoint firecrawl "$FC_BASE" firecrawl_credit_usage "${FIRECRAWL_1_NAME:-firecrawl-1}" "$FIRECRAWL_API_KEY"
fi

if [ "${SEED_LEGACY_NAMES:-0}" = "1" ] &&
  { [ -n "${FIRECRAWL_API_KEYS:-}" ] || [ -n "${FIRECRAWL_1_API_KEY:-${FIRECRAWL_API_KEY:-}}" ]; }; then
  ensure_shared_quota firecrawl gh01 firecrawl_credit_usage || true
fi

# --- Tavily 1..N -------------------------------------------------------------
TAVILY_BASE="${TAVILY_BASE_URL:-$DEFAULT_TAVILY_BASE_URL}"

if [ -n "${TAVILY_1_API_KEY:-}" ] || [ -n "${TAVILY_2_API_KEY:-}" ] || [ -n "${TAVILY_3_API_KEY:-}" ]; then
  seed_shared_endpoint tavily "$TAVILY_BASE" tavily_usage tavily-1 "${TAVILY_1_API_KEY:-}"
  seed_shared_endpoint tavily "$TAVILY_BASE" tavily_usage tavily-2 "${TAVILY_2_API_KEY:-}"
  seed_shared_endpoint tavily "$TAVILY_BASE" tavily_usage tavily-3 "${TAVILY_3_API_KEY:-}"
elif [ -n "${TAVILY_API_KEYS:-}" ]; then
  seed_shared_csv tavily "$TAVILY_BASE" tavily_usage tavily "$TAVILY_API_KEYS"
elif [ -n "${TAVILY_API_KEY:-}" ]; then
  seed_shared_endpoint tavily "$TAVILY_BASE" tavily_usage tavily-1 "$TAVILY_API_KEY"
fi

echo "--- provider-endpoint list ---"
run_adm provider-endpoint list
