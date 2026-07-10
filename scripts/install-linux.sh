#!/usr/bin/env bash
set -euo pipefail

APP_NAME="code-guda-gateway"
SERVICE_NAME="code-guda-gateway.service"
SERVICE_USER="${SERVICE_USER:-code-guda-gateway}"
SERVICE_GROUP="${SERVICE_GROUP:-code-guda-gateway}"
DOMAIN="${DOMAIN:-search.karldigi.dev}"
LISTEN_ADDR="${LISTEN_ADDR:-127.0.0.1:8080}"
REPO_URL="${REPO_URL:-https://github.com/karlorz/code-guda-gateway.git}"
REPO_BRANCH="${REPO_BRANCH:-main}"
INSTALL_ROOT="${INSTALL_ROOT:-}"
RENDER_ONLY=0
SKIP_CADDY=0
SKIP_SOURCE_SYNC=0
SKIP_SERVICE_RESTART="${SKIP_SERVICE_RESTART:-0}"
SKIP_BUILD="${SKIP_BUILD:-0}"
PRINT_PRIVILEGE_MODE=0
TEST_MODE="${CODE_GUDA_GATEWAY_TEST_MODE:-0}"
GO_VERSION="${GO_VERSION:-1.25.0}"
INSTALL_PREREQS="${INSTALL_PREREQS:-1}"
APT_UPDATED=0
ARTIFACT_BASE="${ARTIFACT_BASE:-}"

SRC_DIR="${SRC_DIR:-/opt/code-guda-gateway/src}"
BIN_DIR="${BIN_DIR:-/opt/code-guda-gateway/bin}"
ETC_DIR="${ETC_DIR:-/etc/code-guda-gateway}"
VAR_DIR="${VAR_DIR:-/var/lib/code-guda-gateway}"
SYSTEMD_DIR="${SYSTEMD_DIR:-/etc/systemd/system}"
CADDY_DIR="${CADDY_DIR:-/etc/caddy}"
UPDATE_PATH="${UPDATE_PATH:-/usr/bin/update-code-guda-gateway}"

usage() {
  cat <<'USAGE'
Usage: scripts/install-linux.sh [options]

Options:
  --repo-url URL             Git repository to clone/update.
  --branch NAME              Git branch to deploy.
  --domain HOST              Public Caddy hostname.
  --source-dir PATH          Source checkout path on target host.
  --bin-dir PATH             Binary install directory.
  --etc-dir PATH             Bootstrap/master-key config directory.
  --var-dir PATH             SQLite state directory.
  --skip-caddy               Do not install or update Caddy config.
  --skip-source-sync         Use the existing source directory; do not clone/pull.
  --skip-build               Skip build/install binaries (for controlled tests).
  --skip-service-restart     Install files but do not restart systemd service.
  --render-only              Render config/unit/update files, then exit.
  --artifact-base URL        Public artifact base used by update command.
  --print-privilege-mode     Print root or sudo based on effective uid, then exit.
  -h, --help                 Show this help.

Environment:
  INSTALL_ROOT               Fake-root prefix for tests. /etc writes land under
                             $INSTALL_ROOT/etc, /opt under $INSTALL_ROOT/opt, etc.
  CODE_GUDA_GATEWAY_TEST_MODE=1
                             Bypass Linux/systemd command execution for tests.
  CODE_GUDA_GATEWAY_FAKE_EUID
                             Test-only override for --print-privilege-mode.
  INSTALL_PREREQS=0          Verify prerequisites only; do not install missing tools.
  GO_VERSION=1.25.0          Go version installed from go.dev when go is missing.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-url)
      REPO_URL="${2:?--repo-url requires URL}"
      shift 2
      ;;
    --branch)
      REPO_BRANCH="${2:?--branch requires NAME}"
      shift 2
      ;;
    --domain)
      DOMAIN="${2:?--domain requires HOST}"
      shift 2
      ;;
    --source-dir)
      SRC_DIR="${2:?--source-dir requires PATH}"
      shift 2
      ;;
    --bin-dir)
      BIN_DIR="${2:?--bin-dir requires PATH}"
      shift 2
      ;;
    --etc-dir)
      ETC_DIR="${2:?--etc-dir requires PATH}"
      shift 2
      ;;
    --var-dir)
      VAR_DIR="${2:?--var-dir requires PATH}"
      shift 2
      ;;
    --skip-caddy)
      SKIP_CADDY=1
      shift
      ;;
    --skip-source-sync)
      SKIP_SOURCE_SYNC=1
      shift
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --skip-service-restart)
      SKIP_SERVICE_RESTART=1
      shift
      ;;
    --artifact-base)
      ARTIFACT_BASE="${2:?--artifact-base requires URL}"
      shift 2
      ;;
    --render-only)
      RENDER_ONLY=1
      shift
      ;;
    --print-privilege-mode)
      PRINT_PRIVILEGE_MODE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'unknown option: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TEMPLATE_DIR="$REPO_ROOT/scripts/templates"
export PATH="/usr/local/go/bin:/root/.bun/bin:/usr/local/bin:$PATH"

target_path() {
  local path="$1"
  if [[ -n "$INSTALL_ROOT" ]]; then
    printf '%s%s\n' "${INSTALL_ROOT%/}" "$path"
  else
    printf '%s\n' "$path"
  fi
}

privilege_mode() {
  if [[ "${CODE_GUDA_GATEWAY_FAKE_EUID:-$EUID}" == "0" ]]; then
    printf 'root\n'
  else
    printf 'sudo\n'
  fi
}

if [[ "$PRINT_PRIVILEGE_MODE" == "1" ]]; then
  privilege_mode
  exit 0
fi

run_privileged() {
  if [[ "$TEST_MODE" == "1" ]]; then
    "$@"
    return
  fi
  if [[ "$(privilege_mode)" == "root" ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

run_as_service_user() {
  if [[ "$TEST_MODE" == "1" ]]; then
    "$@"
    return
  fi
  if [[ "$(privilege_mode)" == "root" ]]; then
    runuser -u "$SERVICE_USER" -- "$@"
  else
    sudo -u "$SERVICE_USER" "$@"
  fi
}

require_linux() {
  if [[ "$TEST_MODE" == "1" || -n "$INSTALL_ROOT" ]]; then
    return
  fi
  if [[ "$(uname -s)" != "Linux" ]]; then
    printf 'This installer must run on Linux. Use INSTALL_ROOT with CODE_GUDA_GATEWAY_TEST_MODE=1 for tests.\n' >&2
    exit 1
  fi
  if ! command -v systemctl >/dev/null 2>&1; then
    printf 'systemctl is required on the deployment host.\n' >&2
    exit 1
  fi
}

ensure_prerequisites() {
  if [[ "$TEST_MODE" == "1" || "$RENDER_ONLY" == "1" ]]; then
    return
  fi

  if [[ "$INSTALL_PREREQS" == "1" ]]; then
    install_missing_prerequisites
  fi

  local missing=()
  for cmd in git curl go bun; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      missing+=("$cmd")
    fi
  done
  if [[ "$SKIP_CADDY" != "1" ]] && ! command -v caddy >/dev/null 2>&1; then
    missing+=("caddy")
  fi

  if [[ "${#missing[@]}" -eq 0 ]]; then
    return
  fi

  printf 'Missing required command(s): %s\n' "${missing[*]}" >&2
  printf 'Install Go, Bun, git, curl, and Caddy before running this installer again, or rerun with INSTALL_PREREQS=1.\n' >&2
  exit 1
}

apt_install() {
  if ! command -v apt-get >/dev/null 2>&1; then
    printf 'apt-get is required to install missing Debian prerequisites.\n' >&2
    exit 1
  fi
  if [[ "$APT_UPDATED" == "0" ]]; then
    run_privileged apt-get update
    APT_UPDATED=1
  fi
  run_privileged env DEBIAN_FRONTEND=noninteractive apt-get install -y "$@"
}

install_go() {
  if command -v go >/dev/null 2>&1; then
    return
  fi

  local deb_arch go_arch
  deb_arch="$(dpkg --print-architecture)"
  case "$deb_arch" in
    amd64) go_arch="amd64" ;;
    arm64) go_arch="arm64" ;;
    *)
      printf 'unsupported architecture for automatic Go install: %s\n' "$deb_arch" >&2
      exit 1
      ;;
  esac

  local tmp url
  tmp="$(mktemp -d)"
  url="https://go.dev/dl/go${GO_VERSION}.linux-${go_arch}.tar.gz"
  curl -fsSL "$url" -o "$tmp/go.tgz"
  run_privileged rm -rf /usr/local/go
  run_privileged tar -C /usr/local -xzf "$tmp/go.tgz"
  run_privileged ln -sf /usr/local/go/bin/go /usr/local/bin/go
  run_privileged ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
  rm -rf "$tmp"
}

install_bun() {
  if command -v bun >/dev/null 2>&1; then
    return
  fi

  local tmp
  tmp="$(mktemp)"
  curl -fsSL https://bun.sh/install -o "$tmp"
  run_privileged env BUN_INSTALL=/root/.bun bash "$tmp"
  run_privileged ln -sf /root/.bun/bin/bun /usr/local/bin/bun
  rm -f "$tmp"
}

install_missing_prerequisites() {
  local apt_packages=()
  local need_https_download=0

  for cmd in git curl; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      apt_packages+=("$cmd")
    fi
  done
  if ! command -v tar >/dev/null 2>&1; then
    apt_packages+=("tar")
  fi
  if [[ "$SKIP_CADDY" != "1" ]] && ! command -v caddy >/dev/null 2>&1; then
    apt_packages+=("caddy")
  fi
  if ! command -v go >/dev/null 2>&1 || ! command -v bun >/dev/null 2>&1; then
    need_https_download=1
  fi
  # ca-certificates is a package, not a command; pull it only when we will hit
  # apt or HTTPS bootstrap downloads.
  if [[ "${#apt_packages[@]}" -gt 0 || "$need_https_download" == "1" ]]; then
    apt_packages+=("ca-certificates")
  fi
  if [[ "${#apt_packages[@]}" -gt 0 ]]; then
    apt_install "${apt_packages[@]}"
  fi

  install_go
  install_bun
}

install_dir() {
  local path
  path="$(target_path "$1")"
  run_privileged install -d -m "$2" "$path"
}

write_file() {
  local src="$1"
  local dest="$2"
  local mode="$3"
  local owner="${4:-}"
  local dest_path
  dest_path="$(target_path "$dest")"
  if [[ -n "$owner" && "$TEST_MODE" != "1" ]]; then
    local file_owner="${owner%%:*}"
    local file_group=""
    if [[ "$owner" == *:* ]]; then
      file_group="${owner#*:}"
    fi
    if [[ -n "$file_group" ]]; then
      run_privileged install -m "$mode" -o "$file_owner" -g "$file_group" "$src" "$dest_path"
    else
      run_privileged install -m "$mode" -o "$file_owner" "$src" "$dest_path"
    fi
  else
    run_privileged install -m "$mode" "$src" "$dest_path"
  fi
}

render_template_to() {
  local template="$1"
  local dest="$2"
  local mode="$3"
  local owner="${4:-}"
  local content
  content="$(cat "$template")"
  content="${content//\{\{APP_NAME\}\}/$APP_NAME}"
  content="${content//\{\{SERVICE_NAME\}\}/$SERVICE_NAME}"
  content="${content//\{\{SERVICE_USER\}\}/$SERVICE_USER}"
  content="${content//\{\{SERVICE_GROUP\}\}/$SERVICE_GROUP}"
  content="${content//\{\{DOMAIN\}\}/$DOMAIN}"
  content="${content//\{\{LISTEN_ADDR\}\}/$LISTEN_ADDR}"
  content="${content//\{\{SRC_DIR\}\}/$SRC_DIR}"
  content="${content//\{\{BIN_DIR\}\}/$BIN_DIR}"
  content="${content//\{\{ETC_DIR\}\}/$ETC_DIR}"
  content="${content//\{\{VAR_DIR\}\}/$VAR_DIR}"
  content="${content//\{\{REPO_URL\}\}/$REPO_URL}"
  content="${content//\{\{REPO_BRANCH\}\}/$REPO_BRANCH}"
  content="${content//\{\{ARTIFACT_BASE\}\}/$ARTIFACT_BASE}"
  local tmp
  tmp="$(mktemp)"
  printf '%s\n' "$content" > "$tmp"
  write_file "$tmp" "$dest" "$mode" "$owner"
  rm -f "$tmp"
}

use_source_templates() {
  local from_src
  from_src="$(target_path "$SRC_DIR/scripts/templates")"
  if [[ -d "$from_src" ]]; then
    TEMPLATE_DIR="$from_src"
  fi
}

ensure_service_user() {
  if [[ "$TEST_MODE" == "1" || -n "$INSTALL_ROOT" ]]; then
    return
  fi
  if ! getent group "$SERVICE_GROUP" >/dev/null; then
    run_privileged groupadd --system "$SERVICE_GROUP"
  fi
  if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    run_privileged useradd --system --home-dir "$VAR_DIR" --shell /usr/sbin/nologin --gid "$SERVICE_GROUP" "$SERVICE_USER"
  fi
}

ensure_master_key() {
  local master
  master="$(target_path "$ETC_DIR/master.key")"
  if [[ -e "$master" ]]; then
    return
  fi
  local tmp
  tmp="$(mktemp)"
  head -c 32 /dev/urandom > "$tmp"
  write_file "$tmp" "$ETC_DIR/master.key" 0600 "$SERVICE_USER:$SERVICE_GROUP"
  rm -f "$tmp"
}

ensure_bootstrap_env() {
  local env_path
  env_path="$(target_path "$ETC_DIR/bootstrap.env")"
  if [[ -e "$env_path" ]]; then
    return
  fi
  render_template_to "$TEMPLATE_DIR/bootstrap.env.example" "$ETC_DIR/bootstrap.env" 0640 "root:$SERVICE_GROUP"
}

install_static_files() {
  install_dir "$BIN_DIR" 0755
  install_dir "$ETC_DIR" 0750
  install_dir "$VAR_DIR" 0750
  install_dir "$SYSTEMD_DIR" 0755
  install_dir "$(dirname "$UPDATE_PATH")" 0755

  if [[ "$SKIP_CADDY" != "1" ]]; then
    install_dir "$CADDY_DIR" 0755
    render_template_to "$TEMPLATE_DIR/Caddyfile.code-guda-gateway" "$CADDY_DIR/Caddyfile.code-guda-gateway" 0644
  fi

  render_template_to "$TEMPLATE_DIR/code-guda-gateway.service" "$SYSTEMD_DIR/$SERVICE_NAME" 0644
  render_template_to "$TEMPLATE_DIR/update-code-guda-gateway" "$UPDATE_PATH" 0755
  ensure_bootstrap_env
  ensure_master_key

  if [[ "$TEST_MODE" != "1" && -z "$INSTALL_ROOT" ]]; then
    run_privileged chown "$SERVICE_USER:$SERVICE_GROUP" "$VAR_DIR"
    run_privileged chown "root:$SERVICE_GROUP" "$ETC_DIR"
  fi
}

sync_source() {
  if [[ "$SKIP_SOURCE_SYNC" == "1" ]]; then
    if [[ ! -d "$(target_path "$SRC_DIR")" ]]; then
      printf 'source directory does not exist: %s\n' "$SRC_DIR" >&2
      exit 1
    fi
    return
  fi

  local host_src
  host_src="$(target_path "$SRC_DIR")"
  if [[ -d "$host_src/.git" ]]; then
    run_privileged git -C "$host_src" fetch origin "$REPO_BRANCH" --prune
    run_privileged git -C "$host_src" checkout "$REPO_BRANCH"
    run_privileged git -C "$host_src" pull --ff-only origin "$REPO_BRANCH"
  else
    install_dir "$(dirname "$SRC_DIR")" 0755
    run_privileged git clone --branch "$REPO_BRANCH" "$REPO_URL" "$host_src"
  fi
}

build_and_install() {
  if [[ "$SKIP_BUILD" == "1" ]]; then
    return
  fi
  local host_src host_bin
  host_src="$(target_path "$SRC_DIR")"
  host_bin="$(target_path "$BIN_DIR")"
  (cd "$host_src" && run_privileged ./scripts/build.sh)
  run_privileged install -m 0755 "$host_src/guda-gateway" "$host_bin/guda-gateway"
  run_privileged install -m 0755 "$host_src/guda-gateway-admin" "$host_bin/guda-gateway-admin"
}

run_migrations() {
  if [[ "$TEST_MODE" == "1" || "$SKIP_BUILD" == "1" ]]; then
    return
  fi
  run_as_service_user "$(target_path "$BIN_DIR/guda-gateway-admin")" \
    --db "$VAR_DIR/gateway.db" \
    --master-key "$ETC_DIR/master.key" \
    db migrate
}

install_caddy_import() {
  if [[ "$SKIP_CADDY" == "1" || "$TEST_MODE" == "1" || -n "$INSTALL_ROOT" ]]; then
    return
  fi

  local caddyfile="$CADDY_DIR/Caddyfile"
  local import_line="import $CADDY_DIR/Caddyfile.code-guda-gateway"
  if [[ ! -f "$caddyfile" ]]; then
    printf '%s\n' "$import_line" | run_privileged tee "$caddyfile" >/dev/null
  elif ! grep -Fxq "$import_line" "$caddyfile"; then
    local backup
    backup="$caddyfile.bak.$(date +%Y%m%d%H%M%S)"
    run_privileged cp "$caddyfile" "$backup"
    printf '\n%s\n' "$import_line" | run_privileged tee -a "$caddyfile" >/dev/null
  fi

  run_privileged caddy validate --config "$caddyfile"
  run_privileged systemctl reload caddy
}

install_systemd_service() {
  if [[ "$TEST_MODE" == "1" || -n "$INSTALL_ROOT" || "$SKIP_SERVICE_RESTART" == "1" ]]; then
    return
  fi
  run_privileged systemctl daemon-reload
  run_privileged systemctl enable "$SERVICE_NAME"
  run_privileged systemctl restart "$SERVICE_NAME"
}

main() {
  require_linux
  ensure_prerequisites
  ensure_service_user

  if [[ "$RENDER_ONLY" == "1" ]]; then
    install_static_files
    printf 'Rendered deployment files under %s\n' "${INSTALL_ROOT:-/}"
    return
  fi

  # Sync first so curl-bootstrap and in-place updates render templates/build
  # from the checkout being installed, not from the invoking script path.
  sync_source
  use_source_templates
  install_static_files
  build_and_install
  run_migrations
  install_systemd_service
  install_caddy_import

  printf 'Installed %s from %s branch %s\n' "$APP_NAME" "$REPO_URL" "$REPO_BRANCH"
  printf 'Service: %s\n' "$SERVICE_NAME"
  printf 'Public URL: https://%s\n' "$DOMAIN"
}

main "$@"
