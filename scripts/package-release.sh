#!/usr/bin/env bash
set -euo pipefail

VERSION=""
REVISION=""
ARTIFACT_BASE=""
RELEASE_BASE="https://github.com/karlorz/code-guda-gateway/releases/download"
OUT_DIR="dist"
PLATFORMS=("linux-arm64")
SKIP_BUILD=0
TEST_MODE="${CODE_GUDA_PACKAGE_TEST_MODE:-0}"

usage() {
  cat <<'USAGE'
Usage: scripts/package-release.sh --version VERSION --revision REVISION --artifact-base URL [options]

Options:
  --version VERSION       Release version such as v0.3.1.
  --revision SHA          Source revision embedded into the artifact.
  --artifact-base URL     Public raw base URL where install.sh and stable are served.
  --release-base URL      GitHub Releases download base URL.
  --out-dir DIR           Output directory, default dist.
  --platform NAME         Platform to package; repeatable. Supported: linux-arm64, linux-amd64.
  --skip-build            Use existing binaries in the repository root.
  -h, --help              Show help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="${2:?--version requires VERSION}"; shift 2 ;;
    --revision) REVISION="${2:?--revision requires SHA}"; shift 2 ;;
    --artifact-base) ARTIFACT_BASE="${2:?--artifact-base requires URL}"; shift 2 ;;
    --release-base) RELEASE_BASE="${2:?--release-base requires URL}"; shift 2 ;;
    --out-dir) OUT_DIR="${2:?--out-dir requires DIR}"; shift 2 ;;
    --platform) PLATFORMS+=("${2:?--platform requires NAME}"); shift 2 ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'unknown option: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "$VERSION" ]] || { printf -- '--version is required\n' >&2; exit 2; }
[[ -n "$REVISION" ]] || { printf -- '--revision is required\n' >&2; exit 2; }
[[ -n "$ARTIFACT_BASE" ]] || { printf -- '--artifact-base is required\n' >&2; exit 2; }

case "$VERSION" in
  v[0-9]*.[0-9]*.[0-9]*|v[0-9]*.[0-9]*.[0-9]*-*) ;;
  *) printf 'invalid version: %s\n' "$VERSION" >&2; exit 2 ;;
esac

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR_ABS="$(mkdir -p "$OUT_DIR" && cd "$OUT_DIR" && pwd)"

build_for_platform() {
  local platform="$1" goos goarch
  case "$platform" in
    linux-arm64) goos=linux; goarch=arm64 ;;
    linux-amd64) goos=linux; goarch=amd64 ;;
    *) printf 'unsupported platform: %s\n' "$platform" >&2; exit 2 ;;
  esac

  if [[ "$TEST_MODE" == "1" ]]; then
    mkdir -p "$ROOT/.release-test/$platform"
    printf '#!/usr/bin/env bash\nprintf "guda-gateway test %s\\n"\n' "$platform" > "$ROOT/.release-test/$platform/guda-gateway"
    printf '#!/usr/bin/env bash\nprintf "guda-gateway-admin test %s\\n"\n' "$platform" > "$ROOT/.release-test/$platform/guda-gateway-admin"
    chmod +x "$ROOT/.release-test/$platform/guda-gateway" "$ROOT/.release-test/$platform/guda-gateway-admin"
    printf '%s\n' "$ROOT/.release-test/$platform"
    return
  fi

  if [[ "$SKIP_BUILD" == "1" ]]; then
    printf '%s\n' "$ROOT"
    return
  fi

  mkdir -p "$ROOT/dist/build/$platform"
  (
    cd "$ROOT/web/admin"
    bun install --frozen-lockfile
    bun run build
  )
  rm -rf "$ROOT/internal/adminweb/assets/dist"
  mkdir -p "$ROOT/internal/adminweb/assets/dist"
  cp -R "$ROOT/web/admin/dist/." "$ROOT/internal/adminweb/assets/dist/"
  printf '%s\n' 'stable directory marker for go:embed on fresh checkout' > "$ROOT/internal/adminweb/assets/dist/.keep"
  (
    cd "$ROOT"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -buildvcs=false -o "dist/build/$platform/guda-gateway" ./cmd/guda-gateway
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -buildvcs=false -o "dist/build/$platform/guda-gateway-admin" ./cmd/guda-gateway-admin
  )
  printf '%s\n' "$ROOT/dist/build/$platform"
}

stage_artifact() {
  local platform="$1" bin_src="$2" stage="$3"
  rm -rf "$stage"
  mkdir -p "$stage/bin" "$stage/scripts/templates"
  printf '%s\n' "$VERSION" > "$stage/VERSION"
  printf '%s\n' "$REVISION" > "$stage/REVISION"
  install -m 0755 "$bin_src/guda-gateway" "$stage/bin/guda-gateway"
  install -m 0755 "$bin_src/guda-gateway-admin" "$stage/bin/guda-gateway-admin"
  install -m 0755 "$ROOT/scripts/install-linux.sh" "$stage/scripts/install-linux.sh"
  install -m 0644 "$ROOT/scripts/templates/bootstrap.env.example" "$stage/scripts/templates/bootstrap.env.example"
  install -m 0644 "$ROOT/scripts/templates/code-guda-gateway.service" "$stage/scripts/templates/code-guda-gateway.service"
  install -m 0644 "$ROOT/scripts/templates/Caddyfile.code-guda-gateway" "$stage/scripts/templates/Caddyfile.code-guda-gateway"
  install -m 0755 "$ROOT/scripts/templates/update-code-guda-gateway" "$stage/scripts/templates/update-code-guda-gateway"

  if find "$stage" \( -name '.git' -o -name '._*' -o -name '.DS_Store' \) -print -quit | grep -q .; then
    printf 'forbidden metadata found in staged artifact\n' >&2
    find "$stage" \( -name '.git' -o -name '._*' -o -name '.DS_Store' \) >&2
    exit 1
  fi

  if find "$stage" -type f ! -path '*/bin/*' -exec grep -E 'gat_|gsk_|tvly-|xai-|fc-|Bearer |api_key=' {} + >/dev/null 2>&1; then
    printf 'potential secret material found in staged artifact\n' >&2
    exit 1
  fi
}

render_installer() {
  local out="$1"
  sed \
    -e "s#__CODE_GUDA_ARTIFACT_BASE__#${ARTIFACT_BASE%/}#g" \
    -e "s#__CODE_GUDA_RELEASE_BASE__#${RELEASE_BASE%/}#g" \
    "$ROOT/scripts/guest-install-code-guda-gateway.sh" > "$out"
  chmod +x "$out"
}

VERSION_DIR="$OUT_DIR_ABS/$VERSION"
mkdir -p "$VERSION_DIR"
render_installer "$OUT_DIR_ABS/install.sh"
printf '%s\n' "$VERSION" > "$OUT_DIR_ABS/stable"

for platform in "${PLATFORMS[@]}"; do
  [[ -n "$platform" ]] || continue
  bin_src="$(build_for_platform "$platform")"
  stage="$OUT_DIR_ABS/.stage/$platform"
  stage_artifact "$platform" "$bin_src" "$stage"
  tarball="code-guda-gateway-${VERSION}-${platform}.tar.gz"
  tar -C "$stage" --exclude='.git' --exclude='._*' --exclude='.DS_Store' -czf "$VERSION_DIR/$tarball" .
done

(
  cd "$VERSION_DIR"
  rm -f SHA256SUMS
  for f in code-guda-gateway-"$VERSION"-*.tar.gz; do
    sha256sum "$f" >> SHA256SUMS
  done
)

printf 'release package written to %s\n' "$OUT_DIR_ABS"