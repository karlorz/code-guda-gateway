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

DIST="$TMP/dist"
FAKE_ROOT="$TMP/root"

CODE_GUDA_PACKAGE_TEST_MODE=1 "$ROOT/scripts/package-release.sh" \
  --version v9.9.9-test \
  --revision abc123def456 \
  --artifact-base "file://$DIST" \
  --release-base "file://$DIST" \
  --out-dir "$DIST" \
  --platform linux-arm64

CODE_GUDA_INSTALL_TEST_MODE=1 \
CODE_GUDA_FAKE_UNAME_S=Linux \
CODE_GUDA_FAKE_UNAME_M=aarch64 \
INSTALL_ROOT="$FAKE_ROOT" \
  bash "$DIST/install.sh" \
    --artifact-base "file://$DIST" \
    --release-base "file://$DIST" \
    --version v9.9.9-test \
    --domain search.karldigi.dev \
    --skip-caddy \
    --skip-service-restart

assert_file "$FAKE_ROOT/opt/code-guda-gateway/releases/v9.9.9-test/VERSION"
assert_file "$FAKE_ROOT/opt/code-guda-gateway/bin/guda-gateway"
assert_file "$FAKE_ROOT/opt/code-guda-gateway/bin/guda-gateway-admin"
assert_file "$FAKE_ROOT/etc/code-guda-gateway/bootstrap.env"
assert_file "$FAKE_ROOT/etc/code-guda-gateway/master.key"
assert_file "$FAKE_ROOT/usr/bin/update-code-guda-gateway"
assert_file "$FAKE_ROOT/opt/code-guda-gateway/current/VERSION"

[[ "$(cat "$FAKE_ROOT/opt/code-guda-gateway/current/VERSION")" == "v9.9.9-test" ]] || fail "current VERSION mismatch"
assert_contains "$FAKE_ROOT/usr/bin/update-code-guda-gateway" "curl -fsSL"
assert_contains "$FAKE_ROOT/usr/bin/update-code-guda-gateway" "install.sh"
if grep -E 'git (fetch|checkout|pull)' "$FAKE_ROOT/usr/bin/update-code-guda-gateway" >/dev/null; then
  fail "update wrapper still contains git pull workflow"
fi

# Second pass: render Caddyfile (no --skip-caddy) into a fresh fake root and
# assert all template placeholders are substituted.
FAKE_ROOT_CADDY="$TMP/root-caddy"
CODE_GUDA_INSTALL_TEST_MODE=1 \
CODE_GUDA_FAKE_UNAME_S=Linux \
CODE_GUDA_FAKE_UNAME_M=aarch64 \
INSTALL_ROOT="$FAKE_ROOT_CADDY" \
  bash "$DIST/install.sh" \
    --artifact-base "file://$DIST" \
    --release-base "file://$DIST" \
    --version v9.9.9-test \
    --domain search.karldigi.dev \
    --skip-service-restart
CADDY_FILE="$FAKE_ROOT_CADDY/etc/caddy/Caddyfile.code-guda-gateway"
assert_file "$CADDY_FILE"
assert_contains "$CADDY_FILE" "search.karldigi.dev"
assert_contains "$CADDY_FILE" "127.0.0.1:8080"
if grep -E '\{\{[A-Z_]+\}\}' "$CADDY_FILE" >/dev/null; then
  fail "Caddyfile contains unresolved template placeholders: $(grep -oE '\{\{[A-Z_]+\}\}' "$CADDY_FILE")"
fi

printf 'guest installer tests passed\n'