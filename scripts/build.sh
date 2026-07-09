#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ADMIN_SRC="$ROOT/web/admin"
EMBED_DIST="$ROOT/internal/adminweb/assets/dist"

command -v bun >/dev/null 2>&1 || { echo "bun is required" >&2; exit 1; }

cd "$ADMIN_SRC"
bun install --frozen-lockfile
bun run build

rm -rf "$EMBED_DIST"
mkdir -p "$EMBED_DIST"
cp -R "$ADMIN_SRC/dist/." "$EMBED_DIST/"
printf '%s\n' 'placeholder so go:embed has a stable directory on fresh checkout' > "$EMBED_DIST/.keep"

cd "$ROOT"
# -buildvcs=false: avoid "error obtaining VCS status" when the source dir is a
# non-git copy (e.g. staged onto a deploy host without .git). With set -e the
# build would otherwise fail loudly here; the flag keeps VCS stamping off so the
# binary is produced regardless of whether .git is present.
CGO_ENABLED="${CGO_ENABLED:-0}" go build -buildvcs=false -o guda-gateway ./cmd/guda-gateway
CGO_ENABLED="${CGO_ENABLED:-0}" go build -buildvcs=false -o guda-gateway-admin ./cmd/guda-gateway-admin
