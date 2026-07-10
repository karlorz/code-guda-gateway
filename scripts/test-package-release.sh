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

assert_not_contains() {
  local file="$1"
  local pattern="$2"
  if grep -Fq -- "$pattern" "$file"; then
    fail "expected $file not to contain: $pattern"
  fi
}

OUT="$TMP/dist"
ARTIFACT_BASE="https://raw.githubusercontent.com/karlorz/code-guda-gateway/main/deploy/code-guda-gateway"
RELEASE_BASE="https://github.com/karlorz/code-guda-gateway/releases/download"

CODE_GUDA_PACKAGE_TEST_MODE=1 "$ROOT/scripts/package-release.sh" \
  --version v9.9.9-test \
  --revision abc123def456 \
  --artifact-base "$ARTIFACT_BASE" \
  --release-base "$RELEASE_BASE" \
  --out-dir "$OUT" \
  --platform linux-arm64

assert_file "$OUT/stable"
assert_file "$OUT/install.sh"
assert_file "$OUT/v9.9.9-test/SHA256SUMS"
assert_file "$OUT/v9.9.9-test/code-guda-gateway-v9.9.9-test-linux-arm64.tar.gz"

[[ "$(cat "$OUT/stable")" == "v9.9.9-test" ]] || fail "stable channel did not point to v9.9.9-test"
assert_contains "$OUT/install.sh" "ARTIFACT_BASE_DEFAULT=\"$ARTIFACT_BASE\""
assert_contains "$OUT/install.sh" "RELEASE_BASE_DEFAULT=\"$RELEASE_BASE\""
assert_contains "$OUT/install.sh" "code-guda-gateway"
BAD_TASK_WORD="TO""DO"
BAD_UNKNOWN_WORD="T""BD"
assert_not_contains "$OUT/install.sh" "$BAD_TASK_WORD"
assert_not_contains "$OUT/install.sh" "$BAD_UNKNOWN_WORD"

tar -tzf "$OUT/v9.9.9-test/code-guda-gateway-v9.9.9-test-linux-arm64.tar.gz" > "$TMP/list.txt"
assert_contains "$TMP/list.txt" "VERSION"
assert_contains "$TMP/list.txt" "REVISION"
assert_contains "$TMP/list.txt" "bin/guda-gateway"
assert_contains "$TMP/list.txt" "bin/guda-gateway-admin"
assert_contains "$TMP/list.txt" "scripts/install-linux.sh"
assert_contains "$TMP/list.txt" "scripts/templates/code-guda-gateway.service"
assert_not_contains "$TMP/list.txt" ".git"
assert_not_contains "$TMP/list.txt" "._"
assert_not_contains "$TMP/list.txt" ".DS_Store"

(cd "$OUT/v9.9.9-test" && sha256sum -c SHA256SUMS)

printf 'package release tests passed\n'