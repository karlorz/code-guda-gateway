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
  grep -Fq "$pattern" "$file" || fail "expected $file to contain: $pattern"
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

assert_contains "$UPDATE" "REPO_URL=\"https://example.invalid/karlorz/code-guda-gateway.git\""
assert_contains "$UPDATE" "REPO_BRANCH=\"main\""
assert_contains "$UPDATE" "SRC_DIR=\"/opt/code-guda-gateway/src\""
assert_contains "$UPDATE" 'exec "$SRC_DIR/scripts/install-linux.sh"'

[[ "$(wc -c < "$MASTER" | tr -d ' ')" == "32" ]] || fail "master key should be 32 bytes"

printf 'existing bootstrap\n' > "$BOOTSTRAP"
printf '12345678901234567890123456789012' > "$MASTER"

CODE_GUDA_GATEWAY_TEST_MODE=1 INSTALL_ROOT="$FAKE_ROOT" \
  "$ROOT/scripts/install-linux.sh" --render-only >/dev/null

assert_equals "existing bootstrap" "$(cat "$BOOTSTRAP")"
assert_equals "12345678901234567890123456789012" "$(cat "$MASTER")"

assert_equals "root" "$(CODE_GUDA_GATEWAY_FAKE_EUID=0 "$ROOT/scripts/install-linux.sh" --print-privilege-mode)"
assert_equals "sudo" "$(CODE_GUDA_GATEWAY_FAKE_EUID=501 "$ROOT/scripts/install-linux.sh" --print-privilege-mode)"

printf 'installer tests passed\n'
