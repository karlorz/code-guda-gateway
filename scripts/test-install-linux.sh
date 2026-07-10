#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

assert_file() {
  [[ -f "$1" ]] || fail "expected file $1"
}

assert_contains() {
  local file="$1"
  local pattern="$2"
  grep -Fq -- "$pattern" "$file" || fail "expected $file to contain: $pattern"
}

assert_equals() {
  local want="$1"
  local got="$2"
  [[ "$want" == "$got" ]] || fail "expected [$want], got [$got]"
}

FAKE_ROOT="$TMP/root"

CODE_GUDA_GATEWAY_TEST_MODE=1 INSTALL_ROOT="$FAKE_ROOT" \
  "$ROOT/scripts/install-linux.sh" \
  --render-only \
  --repo-url https://example.invalid/karlorz/code-guda-gateway.git \
  --branch main \
  --artifact-base https://raw.githubusercontent.com/karlorz/code-guda-gateway/main/deploy/code-guda-gateway \
  --domain search.karldigi.dev >/dev/null

SERVICE="$FAKE_ROOT/etc/systemd/system/code-guda-gateway.service"
BOOTSTRAP="$FAKE_ROOT/etc/code-guda-gateway/bootstrap.env"
MASTER="$FAKE_ROOT/etc/code-guda-gateway/master.key"
CADDY="$FAKE_ROOT/etc/caddy/Caddyfile.code-guda-gateway"
UPDATE="$FAKE_ROOT/usr/bin/update-code-guda-gateway"

assert_file "$SERVICE"
assert_file "$BOOTSTRAP"
assert_file "$MASTER"
assert_file "$CADDY"
assert_file "$UPDATE"

assert_contains "$SERVICE" "EnvironmentFile=/etc/code-guda-gateway/bootstrap.env"
assert_contains "$SERVICE" "ExecStart=/opt/code-guda-gateway/bin/guda-gateway"
assert_contains "$SERVICE" "User=code-guda-gateway"
assert_contains "$SERVICE" "ReadWritePaths=/var/lib/code-guda-gateway /etc/code-guda-gateway"

assert_contains "$BOOTSTRAP" "ADDR=127.0.0.1:8080"
assert_contains "$BOOTSTRAP" "DB_PATH=/var/lib/code-guda-gateway/gateway.db"
assert_contains "$BOOTSTRAP" "GUDA_MASTER_KEY_PATH=/etc/code-guda-gateway/master.key"
assert_contains "$BOOTSTRAP" "GUDA_ADMIN_COOKIE_SECURE=true"

assert_contains "$CADDY" "search.karldigi.dev {"
assert_contains "$CADDY" "reverse_proxy 127.0.0.1:8080"

assert_contains "$UPDATE" "ARTIFACT_BASE="
assert_contains "$UPDATE" "curl -fsSL"
assert_contains "$UPDATE" "install.sh"
assert_contains "$UPDATE" "raw.githubusercontent.com/karlorz/code-guda-gateway/main/deploy/code-guda-gateway"
if grep -E 'git (fetch|checkout|pull)' "$UPDATE" >/dev/null; then
  fail "update command must not depend on git pull"
fi

[[ "$(wc -c < "$MASTER" | tr -d ' ')" == "32" ]] || fail "master key should be 32 bytes"

printf 'existing bootstrap\n' > "$BOOTSTRAP"
printf '12345678901234567890123456789012' > "$MASTER"

CODE_GUDA_GATEWAY_TEST_MODE=1 INSTALL_ROOT="$FAKE_ROOT" \
  "$ROOT/scripts/install-linux.sh" --render-only >/dev/null

assert_equals "existing bootstrap" "$(cat "$BOOTSTRAP")"
assert_equals "12345678901234567890123456789012" "$(cat "$MASTER")"

assert_equals "root" "$(CODE_GUDA_GATEWAY_FAKE_EUID=0 "$ROOT/scripts/install-linux.sh" --print-privilege-mode)"
assert_equals "sudo" "$(CODE_GUDA_GATEWAY_FAKE_EUID=501 "$ROOT/scripts/install-linux.sh" --print-privilege-mode)"

# Custom path flags must flow into rendered bootstrap.env
CUSTOM_ROOT="$TMP/custom"
CODE_GUDA_GATEWAY_TEST_MODE=1 INSTALL_ROOT="$CUSTOM_ROOT" \
  "$ROOT/scripts/install-linux.sh" \
  --render-only \
  --etc-dir /etc/custom-guda \
  --var-dir /var/lib/custom-guda \
  --domain custom.example >/dev/null

assert_contains "$CUSTOM_ROOT/etc/custom-guda/bootstrap.env" "DB_PATH=/var/lib/custom-guda/gateway.db"
assert_contains "$CUSTOM_ROOT/etc/custom-guda/bootstrap.env" "GUDA_MASTER_KEY_PATH=/etc/custom-guda/master.key"
assert_contains "$CUSTOM_ROOT/etc/caddy/Caddyfile.code-guda-gateway" "custom.example {"

printf 'installer tests passed\n'
