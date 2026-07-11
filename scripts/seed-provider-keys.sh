#!/usr/bin/env bash
# Seed provider endpoint pairs + quota sidecars into the guda-gateway SQLite DB.
# Bash-only (no zsh arrays). Never echoes raw keys or places them on argv.
#
# Usage:
#   set -a; . ~/.secrets/guda-gateway.env; set +a
#   export DB_PATH=... GUDA_MASTER_KEY_PATH=...
#   ./scripts/seed-provider-keys.sh [guda-gateway-admin-path]
#
# Topology (canonical names — identifiers only, not pool roles):
#   grok-1       inference → GROK_1_BASE_URL (default https://new.karldigi.dev/v1)
#                quota     → separate_credentials @ https://grok.karldigi.dev
#   tavily-1..N  inference → TAVILY_BASE_URL (official); quota endpoint_credentials
#   firecrawl-1  inference → FIRECRAWL_BASE_URL (official); quota endpoint_credentials
#
# Idempotent per (provider, name): skips when name already exists.
# Existing rows can still get quota via set-quota + rotate-quota-key after seed.
set -euo pipefail

ADM="${1:-./guda-gateway-admin}"
DB="${DB_PATH:?DB_PATH must be set}"
MK="${GUDA_MASTER_KEY_PATH:?GUDA_MASTER_KEY_PATH must be set}"

DEFAULT_GROK_BASE_URL="https://new.karldigi.dev/v1"
DEFAULT_GROK_QUOTA_BASE_URL="https://grok.karldigi.dev"
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
  # Prints: mode<TAB>configured<TAB>quota_base_url  (configured = true/false)
  local provider="$1" name="$2"
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 -separator $'\t' "$DB" \
      "SELECT COALESCE(quota_mode,''), CASE WHEN encrypted_quota_key IS NOT NULL AND encrypted_quota_key!='' THEN 'true' ELSE 'false' END, COALESCE(quota_base_url,'')
       FROM provider_keys
       WHERE provider='${provider//\'/\'\'}' AND name='${name//\'/\'\'}'
         AND (archived_at IS NULL OR archived_at='')
       ORDER BY id LIMIT 1;"
    return 0
  fi
  printf '\tfalse\t'
}

write_secret_file() {
  local content="$1" f
  f="$(mktemp)"
  umask 077
  printf '%s' "$content" >"$f"
  chmod 600 "$f"
  printf '%s' "$f"
}

add_endpoint() {
  # add_endpoint provider name base_url raw_key [quota_mode] [quota_flow] [quota_base_url] [quota_key]
  local provider="$1" name="$2" base_url="$3" key="$4"
  local quota_mode="${5:-}" quota_flow="${6:-}" quota_base_url="${7:-}" quota_key="${8:-}"
  local out rc=0 args qfile=""

  [ -z "$key" ] && { echo "skip $provider/$name: empty inference key" >&2; return 0; }

  args=(provider-endpoint add --provider "$provider" --name "$name" --base-url "$base_url")
  if [ -n "$quota_mode" ]; then
    args+=(--quota-mode "$quota_mode")
  fi
  if [ -n "$quota_flow" ]; then
    args+=(--quota-flow "$quota_flow")
  fi
  if [ -n "$quota_base_url" ]; then
    args+=(--quota-base-url "$quota_base_url")
  fi
  if [ -n "$quota_key" ]; then
    if [ "$quota_mode" != "separate_credentials" ]; then
      echo "error $provider/$name: quota key only valid with separate_credentials" >&2
      return 1
    fi
    qfile="$(write_secret_file "$quota_key")"
    args+=(--quota-key-file "$qfile")
  fi

  out=$(printf '%s' "$key" | run_adm "${args[@]}" 2>&1) || rc=$?
  [ -n "$qfile" ] && rm -f "$qfile"

  if [ "$rc" -ne 0 ]; then
    if printf '%s' "$out" | grep -q 'name already exists'; then
      echo "skip $provider/$name: already exists" >&2
      if [ "$quota_mode" = "separate_credentials" ] && [ -n "$quota_base_url" ]; then
        ensure_separate_quota "$provider" "$name" "$quota_flow" "$quota_base_url" "$quota_key"
      elif [ "$quota_mode" = "endpoint_credentials" ] && [ -n "$quota_flow" ]; then
        ensure_shared_quota "$provider" "$name" "$quota_flow"
      fi
      return 0
    fi
    printf '%s\n' "$out" >&2
    return "$rc"
  fi
  printf '%s\n' "$out"
}

ensure_separate_quota() {
  local provider="$1" name="$2" flow="$3" quota_url="$4" quota_key="$5"
  local id mode configured meta current_url

  id="$(endpoint_id "$provider" "$name")"
  [ -z "$id" ] && return 0

  meta="$(endpoint_quota_meta "$provider" "$name")"
  mode="$(printf '%s' "$meta" | cut -f1)"
  configured="$(printf '%s' "$meta" | cut -f2)"
  current_url="$(printf '%s' "$meta" | cut -f3)"

  # Only mutate when mode/URL are not already correct (idempotent re-runs).
  if [ "$mode" != "separate_credentials" ] || { [ -n "$quota_url" ] && [ "$current_url" != "$quota_url" ]; }; then
    if run_adm provider-endpoint set-quota \
      --id "$id" \
      --mode separate_credentials \
      --flow "${flow:-grok2api_admin}" \
      --base-url "$quota_url" >/dev/null; then
      echo "set-quota $provider/$name id=$id separate @ $quota_url" >&2
    else
      echo "set-quota $provider/$name id=$id failed" >&2
      return 0
    fi
  fi

  # Re-read configured after possible set-quota (entering separate may clear key).
  meta="$(endpoint_quota_meta "$provider" "$name")"
  configured="$(printf '%s' "$meta" | cut -f2)"

  if [ -n "$quota_key" ] && [ "$configured" != "true" ]; then
    if printf '%s' "$quota_key" | run_adm provider-endpoint rotate-quota-key --id "$id" >/dev/null; then
      echo "rotate-quota-key $provider/$name id=$id ok" >&2
    else
      echo "rotate-quota-key $provider/$name id=$id failed" >&2
    fi
  fi
}


ensure_shared_quota() {
  local provider="$1" name="$2" flow="$3"
  local id mode meta

  id="$(endpoint_id "$provider" "$name")"
  [ -z "$id" ] && return 0
  meta="$(endpoint_quota_meta "$provider" "$name")"
  mode="$(printf '%s' "$meta" | cut -f1)"
  if [ "$mode" != "endpoint_credentials" ]; then
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
GROK_QMODE="${GROK_1_QUOTA_MODE:-separate_credentials}"
GROK_QFLOW="${GROK_1_QUOTA_FLOW:-grok2api_admin}"
GROK_QURL="${GROK_1_QUOTA_BASE_URL:-${grok2api_admin_base_url:-$DEFAULT_GROK_QUOTA_BASE_URL}}"
GROK_QKEY="${GROK_1_QUOTA_KEY:-${grok2api_admin_key:-}}"

add_endpoint grok "$GROK_NAME" "$GROK_BASE" "$GROK_KEY" \
  "$GROK_QMODE" "$GROK_QFLOW" "$GROK_QURL" "$GROK_QKEY"

# If create was skipped (name exists), ensure quota sidecar matches env.
if [ -n "$GROK_QURL" ]; then
  ensure_separate_quota grok "$GROK_NAME" "$GROK_QFLOW" "$GROK_QURL" "$GROK_QKEY" || true
fi

# Optional: only when SEED_LEGACY_NAMES=1, also patch common pre-rename rows.
if [ "${SEED_LEGACY_NAMES:-0}" = "1" ] && [ -n "$GROK_QURL" ]; then
  for legacy in karldigi; do
    [ "$legacy" = "$GROK_NAME" ] && continue
    ensure_separate_quota grok "$legacy" "$GROK_QFLOW" "$GROK_QURL" "$GROK_QKEY" || true
  done
fi

# --- Firecrawl-1 -------------------------------------------------------------
FC_NAME="${FIRECRAWL_1_NAME:-firecrawl-1}"
FC_BASE="${FIRECRAWL_BASE_URL:-$DEFAULT_FIRECRAWL_BASE_URL}"
FC_KEY="${FIRECRAWL_1_API_KEY:-${FIRECRAWL_API_KEY:-}}"
add_endpoint firecrawl "$FC_NAME" "$FC_BASE" "$FC_KEY" \
  endpoint_credentials firecrawl_credit_usage "" ""
if [ -n "$FC_KEY" ]; then
  ensure_shared_quota firecrawl "$FC_NAME" firecrawl_credit_usage || true
  if [ "${SEED_LEGACY_NAMES:-0}" = "1" ]; then
    ensure_shared_quota firecrawl gh01 firecrawl_credit_usage || true
  fi
fi

# --- Tavily 1..N -------------------------------------------------------------
TAVILY_BASE="${TAVILY_BASE_URL:-$DEFAULT_TAVILY_BASE_URL}"

seed_tavily_named() {
  local name="$1" key="$2"
  [ -z "$key" ] && return 0
  add_endpoint tavily "$name" "$TAVILY_BASE" "$key" \
    endpoint_credentials tavily_usage "" ""
  ensure_shared_quota tavily "$name" tavily_usage || true
}

if [ -n "${TAVILY_1_API_KEY:-}" ] || [ -n "${TAVILY_2_API_KEY:-}" ] || [ -n "${TAVILY_3_API_KEY:-}" ]; then
  seed_tavily_named tavily-1 "${TAVILY_1_API_KEY:-}"
  seed_tavily_named tavily-2 "${TAVILY_2_API_KEY:-}"
  seed_tavily_named tavily-3 "${TAVILY_3_API_KEY:-}"
elif [ -n "${TAVILY_API_KEYS:-}" ]; then
  i=1
  while IFS= read -r k; do
    k="${k//\"/}"; k="${k// /}"
    [ -z "$k" ] && continue
    seed_tavily_named "tavily-$i" "$k"
    i=$((i + 1))
  done < <(printf '%s\n' "$TAVILY_API_KEYS" | tr ',' '\n')
elif [ -n "${TAVILY_API_KEY:-}" ]; then
  seed_tavily_named tavily-1 "$TAVILY_API_KEY"
fi

echo "--- provider-endpoint list ---"
run_adm provider-endpoint list
