#!/usr/bin/env bash
set -euo pipefail

ARTIFACT_BASE_DEFAULT="__CODE_GUDA_ARTIFACT_BASE__"
RELEASE_BASE_DEFAULT="__CODE_GUDA_RELEASE_BASE__"
APP_NAME="code-guda-gateway"
SERVICE_NAME="code-guda-gateway.service"
DOMAIN="${CODE_GUDA_DOMAIN:-search.karldigi.dev}"
VERSION="${CODE_GUDA_VERSION:-}"
CHANNEL="${CODE_GUDA_CHANNEL:-stable}"
ARTIFACT_BASE="${CODE_GUDA_ARTIFACT_BASE:-$ARTIFACT_BASE_DEFAULT}"
RELEASE_BASE="${CODE_GUDA_RELEASE_BASE:-$RELEASE_BASE_DEFAULT}"
INSTALL_ROOT="${INSTALL_ROOT:-}"
INSTALL_BASE="${CODE_GUDA_INSTALL_BASE:-/opt/code-guda-gateway}"
ETC_DIR="${CODE_GUDA_ETC_DIR:-/etc/code-guda-gateway}"
VAR_DIR="${CODE_GUDA_VAR_DIR:-/var/lib/code-guda-gateway}"
SYSTEMD_DIR="${CODE_GUDA_SYSTEMD_DIR:-/etc/systemd/system}"
CADDY_DIR="${CODE_GUDA_CADDY_DIR:-/etc/caddy}"
UPDATE_PATH="${CODE_GUDA_UPDATE_PATH:-/usr/bin/update-code-guda-gateway}"
LISTEN_ADDR="${CODE_GUDA_LISTEN_ADDR:-127.0.0.1:8080}"
SKIP_CADDY="${CODE_GUDA_SKIP_CADDY:-0}"
SKIP_PREREQS="${CODE_GUDA_SKIP_PREREQS:-0}"
SKIP_SERVICE_RESTART="${CODE_GUDA_SKIP_SERVICE_RESTART:-0}"
DRY_RUN=0
TEST_MODE="${CODE_GUDA_INSTALL_TEST_MODE:-0}"

usage() {
  cat <<'USAGE'
Usage: install.sh [options]

Options:
  --version VERSION           Install a concrete version such as v0.3.1.
  --channel NAME              Resolve version from channel file, default stable.
  --artifact-base URL         Public artifact base URL.
  --release-base URL          Release asset base URL.
  --domain HOST               Caddy hostname, default search.karldigi.dev.
  --skip-caddy                Do not write or reload Caddy config.
  --skip-prereqs              Do not install missing apt prerequisites.
  --skip-service-restart      Install files without restarting systemd.
  --dry-run                   Print resolved install parameters and exit.
  -h, --help                  Show help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="${2:?--version requires VERSION}"; shift 2 ;;
    --channel) CHANNEL="${2:?--channel requires NAME}"; shift 2 ;;
    --artifact-base) ARTIFACT_BASE="${2:?--artifact-base requires URL}"; shift 2 ;;
    --release-base) RELEASE_BASE="${2:?--release-base requires URL}"; shift 2 ;;
    --domain) DOMAIN="${2:?--domain requires HOST}"; shift 2 ;;
    --skip-caddy) SKIP_CADDY=1; shift ;;
    --skip-prereqs) SKIP_PREREQS=1; shift ;;
    --skip-service-restart) SKIP_SERVICE_RESTART=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'unknown option: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

target_path() {
  local path="$1"
  if [[ -n "$INSTALL_ROOT" ]]; then
    printf '%s%s\n' "${INSTALL_ROOT%/}" "$path"
  else
    printf '%s\n' "$path"
  fi
}

run_privileged() {
  if [[ "$TEST_MODE" == "1" || -n "$INSTALL_ROOT" ]]; then
    "$@"
    return
  fi
  if [[ "$EUID" == "0" ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

fetch_to_file() {
  local url="$1" out="$2"
  case "$url" in
    file://*) cp "${url#file://}" "$out" ;;
    http://*|https://*) curl -fsSL "$url" -o "$out" ;;
    *) printf 'unsupported URL: %s\n' "$url" >&2; exit 2 ;;
  esac
}

fetch_to_stdout() {
  local url="$1"
  case "$url" in
    file://*) cat "${url#file://}" ;;
    http://*|https://*) curl -fsSL "$url" ;;
    *) printf 'unsupported URL: %s\n' "$url" >&2; exit 2 ;;
  esac
}

detect_platform() {
  local os arch
  os="${CODE_GUDA_FAKE_UNAME_S:-$(uname -s)}"
  arch="${CODE_GUDA_FAKE_UNAME_M:-$(uname -m)}"
  case "$os" in
    Linux) ;;
    *) printf 'unsupported OS for service install: %s\n' "$os" >&2; exit 2 ;;
  esac
  case "$arch" in
    aarch64|arm64) printf 'linux-arm64\n' ;;
    x86_64|amd64) printf 'linux-amd64\n' ;;
    *) printf 'unsupported architecture: %s\n' "$arch" >&2; exit 2 ;;
  esac
}

resolve_version() {
  if [[ -n "$VERSION" ]]; then
    printf '%s\n' "$VERSION"
    return
  fi
  fetch_to_stdout "${ARTIFACT_BASE%/}/$CHANNEL" | tr -d '[:space:]'
}

install_prereqs() {
  if [[ "$TEST_MODE" == "1" || "$SKIP_PREREQS" == "1" ]]; then
    return
  fi
  if ! command -v apt-get >/dev/null 2>&1; then
    printf 'apt-get is required on production Linux hosts\n' >&2
    exit 1
  fi
  local missing=()
  for cmd in curl tar sha256sum systemctl; do
    command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
  done
  if [[ "${#missing[@]}" -gt 0 ]]; then
    run_privileged apt-get update
    run_privileged env DEBIAN_FRONTEND=noninteractive apt-get install -y curl tar coreutils systemd ca-certificates
  fi
}

verify_checksum() {
  local dir="$1" tarball="$2"
  (
    cd "$dir"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum -c SHA256SUMS --ignore-missing >/dev/null 2>&1 || return 1
    else
      shasum -a 256 -c SHA256SUMS >/dev/null 2>&1 || return 1
    fi
    grep -Fq "  $tarball" SHA256SUMS
  )
}

download_release() {
  local version="$1" platform="$2" tmp="$3"
  local tarball="code-guda-gateway-${version}-${platform}.tar.gz"
  mkdir -p "$tmp"
  fetch_to_file "${RELEASE_BASE%/}/${version}/SHA256SUMS" "$tmp/SHA256SUMS"
  fetch_to_file "${RELEASE_BASE%/}/${version}/${tarball}" "$tmp/$tarball"
  verify_checksum "$tmp" "$tarball"
  printf '%s\n' "$tmp/$tarball"
}

ensure_state_files() {
  run_privileged install -d -m 0755 "$(target_path "$INSTALL_BASE")" "$(target_path "$INSTALL_BASE/releases")" "$(target_path "$INSTALL_BASE/bin")"
  run_privileged install -d -m 0750 "$(target_path "$ETC_DIR")" "$(target_path "$VAR_DIR")"
  if [[ ! -f "$(target_path "$ETC_DIR/master.key")" ]]; then
    local tmp
    tmp="$(mktemp)"
    head -c 32 /dev/urandom > "$tmp"
    run_privileged install -m 0600 "$tmp" "$(target_path "$ETC_DIR/master.key")"
    rm -f "$tmp"
  fi
  if [[ ! -f "$(target_path "$ETC_DIR/bootstrap.env")" ]]; then
    cat > "$(target_path "$ETC_DIR/bootstrap.env")" <<EOF
ADDR=127.0.0.1:8080
DB_PATH=$VAR_DIR/gateway.db
GUDA_MASTER_KEY_PATH=$ETC_DIR/master.key
GUDA_ADMIN_COOKIE_SECURE=true
EOF
    chmod 0640 "$(target_path "$ETC_DIR/bootstrap.env")"
  fi
}

write_update_wrapper() {
  run_privileged install -d -m 0755 "$(dirname "$(target_path "$UPDATE_PATH")")"
  cat > "$(target_path "$UPDATE_PATH")" <<EOF
#!/usr/bin/env bash
set -euo pipefail
exec bash -c "\$(curl -fsSL ${ARTIFACT_BASE%/}/install.sh)" -- "\$@"
EOF
  chmod 0755 "$(target_path "$UPDATE_PATH")"
}

install_release() {
  local version="$1" tarball="$2" release_dir current_dir
  release_dir="$(target_path "$INSTALL_BASE/releases/$version")"
  current_dir="$(target_path "$INSTALL_BASE/current")"
  rm -rf "$release_dir"
  run_privileged install -d -m 0755 "$release_dir"
  tar -xzf "$tarball" -C "$release_dir"
  run_privileged install -m 0755 "$release_dir/bin/guda-gateway" "$(target_path "$INSTALL_BASE/bin/guda-gateway")"
  run_privileged install -m 0755 "$release_dir/bin/guda-gateway-admin" "$(target_path "$INSTALL_BASE/bin/guda-gateway-admin")"
  rm -rf "$current_dir"
  mkdir -p "$current_dir"
  cp "$release_dir/VERSION" "$current_dir/VERSION"
  cp "$release_dir/REVISION" "$current_dir/REVISION"
}

install_systemd_and_caddy() {
  local release_dir
  release_dir="$(target_path "$INSTALL_BASE/releases/$1")"
  run_privileged install -d -m 0755 "$(target_path "$SYSTEMD_DIR")"
  sed \
    -e "s#{{BIN_DIR}}#$INSTALL_BASE/bin#g" \
    -e "s#{{ETC_DIR}}#$ETC_DIR#g" \
    -e "s#{{VAR_DIR}}#$VAR_DIR#g" \
    -e "s#{{SERVICE_USER}}#root#g" \
    -e "s#{{SERVICE_GROUP}}#root#g" \
    "$release_dir/scripts/templates/code-guda-gateway.service" > "$(target_path "$SYSTEMD_DIR/$SERVICE_NAME")"

  if [[ "$SKIP_CADDY" != "1" ]]; then
    run_privileged install -d -m 0755 "$(target_path "$CADDY_DIR")"
    sed \
      -e "s#{{DOMAIN}}#$DOMAIN#g" \
      -e "s#{{LISTEN_ADDR}}#$LISTEN_ADDR#g" \
      "$release_dir/scripts/templates/Caddyfile.code-guda-gateway" > "$(target_path "$CADDY_DIR/Caddyfile.code-guda-gateway")"
  fi
}

restart_and_verify() {
  if [[ "$TEST_MODE" == "1" || -n "$INSTALL_ROOT" || "$SKIP_SERVICE_RESTART" == "1" ]]; then
    return
  fi
  run_privileged systemctl daemon-reload
  run_privileged systemctl enable "$SERVICE_NAME"
  run_privileged "$(target_path "$INSTALL_BASE/bin/guda-gateway-admin")" --db "$VAR_DIR/gateway.db" --master-key "$ETC_DIR/master.key" db migrate
  run_privileged systemctl restart "$SERVICE_NAME"
  systemctl is-active "$SERVICE_NAME" >/dev/null
  local waited=0
  until curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1; do
    if [[ "$waited" -ge 15 ]]; then
      printf 'service did not become healthy within 15s\n' >&2
      return 1
    fi
    sleep 1
    waited=$((waited + 1))
  done
  if [[ "$SKIP_CADDY" != "1" ]]; then
    caddy validate --config "$CADDY_DIR/Caddyfile"
    systemctl reload caddy
  fi
}

cleanup() {
  [[ -n "${tmp:-}" ]] && rm -rf "$tmp"
}

main() {
  local unresolved_artifact="__CODE_GUDA""_ARTIFACT_BASE__"
  local unresolved_release="__CODE_GUDA""_RELEASE_BASE__"
  if [[ -z "$ARTIFACT_BASE" || "$ARTIFACT_BASE" == "$unresolved_artifact" ]]; then
    printf 'artifact base is required; pass --artifact-base or render install.sh with package-release\n' >&2
    exit 2
  fi
  if [[ -z "$RELEASE_BASE" || "$RELEASE_BASE" == "$unresolved_release" ]]; then
    printf 'release base is required; pass --release-base or render install.sh with package-release\n' >&2
    exit 2
  fi
  local platform version tarball
  platform="$(detect_platform)"
  version="$(resolve_version)"
  if [[ "$DRY_RUN" == "1" ]]; then
    printf 'version=%s\nplatform=%s\nartifact_base=%s\nrelease_base=%s\ndomain=%s\n' "$version" "$platform" "$ARTIFACT_BASE" "$RELEASE_BASE" "$DOMAIN"
    return
  fi
  install_prereqs
  tmp="$(mktemp -d)"
  trap cleanup EXIT
  tarball="$(download_release "$version" "$platform" "$tmp")"
  ensure_state_files
  install_release "$version" "$tarball"
  install_systemd_and_caddy "$version"
  write_update_wrapper
  restart_and_verify
  trap - EXIT
  rm -rf "$tmp"
  printf 'Installed code-guda-gateway %s for %s\n' "$version" "$platform"
}

main "$@"