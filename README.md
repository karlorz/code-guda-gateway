# code-guda-gateway

Go HTTP gateway that exposes a `code.guda.studio`-compatible facade for
GrokSearch MCP.

## Contract

The gateway is designed for GrokSearch configurations like:

```bash
GUDA_BASE_URL=http://127.0.0.1:8080
GUDA_API_KEY=<gateway-key-from-cli>
```

Public routes (require `Authorization: Bearer <gateway-key>`):

- `GET /grok/v1/models`
- `POST /grok/v1/chat/completions`
- `POST /tavily/search`
- `POST /tavily/extract`
- `POST /tavily/map`
- `POST /firecrawl/search`
- `POST /firecrawl/scrape`
- `GET /healthz` (no auth)

Admin UI: `http://127.0.0.1:8080/admin` (login with the admin token from CLI).

## Bootstrap configuration (process only)

Gateway keys, provider API keys, and upstream base URLs live in **SQLite**, not
in environment variables. The service only needs these bootstrap settings as
**process environment variables** (the Go binary calls `os.LookupEnv` only; it
does not read files):

| Source | Notes |
|---|---|
| Export in your shell | Typical for local dev (see below). |
| `/etc/code-guda-gateway/bootstrap.env` | Convenience template — must be loaded into the environment before start (e.g. systemd `EnvironmentFile=` on the unit, or `set -a; source …; set +a` for manual runs). |

Variables:

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `127.0.0.1:8080` | Listen address (localhost by default). |
| `DB_PATH` | `/var/lib/code-guda-gateway/gateway.db` | SQLite database path. |
| `GUDA_MASTER_KEY_PATH` | `/etc/code-guda-gateway/master.key` | File used to load or create the encryption master key. |
| `GUDA_ADMIN_COOKIE_SECURE` | `true` | Set to `false` for local plain-HTTP browser testing. |
| `GUDA_PROXY_DEBUG_ATTEMPTS` | unset | Optional local/dev bootstrap for the admin `proxy_debug_attempts` setting. Leave unset in production unless explicitly debugging retry behavior. |

See `scripts/templates/bootstrap.env.example` for a secret-free template.

`GUDA_GATEWAY_KEYS` and provider env vars (`GROK_UPSTREAM_*`, `TAVILY_*`,
`FIRECRAWL_*`) are **not** used. Create gateway keys with
`guda-gateway-admin gateway-key create`.

## First-run setup (production paths)

```bash
# Global flags must come before the subcommand (stdlib flag parsing).
guda-gateway-admin db migrate
guda-gateway-admin token init          # save the printed token once
guda-gateway-admin gateway-key create --name groksearch
guda-gateway-admin provider-key add --provider grok --name primary   # key via stdin
guda-gateway-admin provider-key add --provider tavily --name primary
guda-gateway-admin provider-key add --provider firecrawl --name primary
# Optional: set upstream base URLs via CLI settings commands or the admin UI.

go build -o guda-gateway ./cmd/guda-gateway
./guda-gateway
```

## Linux systemd/Caddy deploy

The production installer builds the embedded-admin release from a host checkout,
preserves existing state, installs systemd, and optionally configures Caddy for:

```text
https://search.karldigi.dev -> 127.0.0.1:8080
```

Default paths:

| Purpose | Path |
|---|---|
| Source checkout | `/opt/code-guda-gateway/src` |
| Binaries | `/opt/code-guda-gateway/bin/guda-gateway`, `/opt/code-guda-gateway/bin/guda-gateway-admin` |
| Bootstrap env | `/etc/code-guda-gateway/bootstrap.env` |
| Master key | `/etc/code-guda-gateway/master.key` |
| SQLite DB | `/var/lib/code-guda-gateway/gateway.db` |
| systemd unit | `/etc/systemd/system/code-guda-gateway.service` |
| Caddy site snippet | `/etc/caddy/Caddyfile.code-guda-gateway` |
| Update command | `/usr/bin/update-code-guda-gateway` |

Run from a root shell or a sudo-capable user:

```bash
curl -fsSL https://raw.githubusercontent.com/karlorz/code-guda-gateway/main/scripts/install-linux.sh -o /tmp/install-code-guda-gateway.sh
bash /tmp/install-code-guda-gateway.sh \
  --repo-url https://github.com/karlorz/code-guda-gateway.git \
  --branch main \
  --domain search.karldigi.dev
```

The installer creates missing directories, the service user, bootstrap env,
master key, systemd unit, Caddy snippet, and update command. It installs missing
build prerequisites by default (`git`, `curl`, Caddy when needed, Go, and Bun);
set `INSTALL_PREREQS=0` to verify-only and fail if tools are absent. It does not
replace an existing SQLite DB, master key, or bootstrap env.

After install, initialize operational credentials on the host. Admin tokens and
gateway keys print once; provider keys and Grok admin keys must be supplied via
stdin, never as command arguments:

```bash
ADM=/opt/code-guda-gateway/bin/guda-gateway-admin
DB=/var/lib/code-guda-gateway/gateway.db
MK=/etc/code-guda-gateway/master.key

"$ADM" --db "$DB" --master-key "$MK" token init
"$ADM" --db "$DB" --master-key "$MK" gateway-key create --name groksearch
printf '%s' "$GROK_UPSTREAM_API_KEY" | "$ADM" --db "$DB" --master-key "$MK" provider-key add --provider grok --name primary
printf '%s' "$TAVILY_API_KEY" | "$ADM" --db "$DB" --master-key "$MK" provider-key add --provider tavily --name primary
printf '%s' "$FIRECRAWL_API_KEY" | "$ADM" --db "$DB" --master-key "$MK" provider-key add --provider firecrawl --name primary
```

Operational checks:

```bash
systemctl status code-guda-gateway --no-pager
systemctl is-enabled code-guda-gateway
caddy validate --config /etc/caddy/Caddyfile
curl -fsS https://search.karldigi.dev/healthz
curl -fsS https://search.karldigi.dev/admin
```

Update in place after the first install:

```bash
update-code-guda-gateway
```

The production update command fetches the public installer from
`CODE_GUDA_ARTIFACT_BASE`, downloads a checksum-verified release artifact,
reruns migrations, and restarts the service while preserving DB, master key,
bootstrap env, admin token hash, provider keys, and gateway keys. The default
artifact base is:

```text
https://raw.githubusercontent.com/karlorz/code-guda-gateway/main/deploy/code-guda-gateway
```

`kr01` does not need GitHub credentials for this path. The target host consumes
only public, secret-free release artifacts from same-repo GitHub Releases.

### Public release artifact deployment

Build public deploy artifacts from a trusted workstation or CI environment:

```bash
REVISION="$(git rev-parse HEAD)"
export CODE_GUDA_ARTIFACT_BASE="https://raw.githubusercontent.com/karlorz/code-guda-gateway/main/deploy/code-guda-gateway"
export CODE_GUDA_RELEASE_BASE="https://github.com/karlorz/code-guda-gateway/releases/download"
scripts/package-release.sh \
  --version v0.3.1 \
  --revision "$REVISION" \
  --artifact-base "$CODE_GUDA_ARTIFACT_BASE" \
  --release-base "$CODE_GUDA_RELEASE_BASE" \
  --out-dir dist \
  --platform linux-arm64
```

The tag-driven GitHub Actions release workflow publishes binary artifacts as
same-repo GitHub Release assets and promotes the raw stable channel file:

```text
github.com/karlorz/code-guda-gateway
├── releases/download/v0.3.1/
│   ├── SHA256SUMS
│   └── code-guda-gateway-v0.3.1-linux-arm64.tar.gz
└── deploy/code-guda-gateway/
    ├── install.sh
    └── stable
```

Run on the target host:

```bash
curl -fsSL "$CODE_GUDA_ARTIFACT_BASE/install.sh" | bash
curl -fsSL "$CODE_GUDA_ARTIFACT_BASE/install.sh" | bash -s -- --version v0.3.1
```

The installer does not clone the private repository and does not read provider
secrets from the artifact. Runtime credentials remain in SQLite and the
host-local master key. If this repository is private, raw and release asset
URLs still require authentication, so the no-token-on-kr01 property requires
public readability for these deploy surfaces.

The source checkout path remains useful for local testing and emergency
operator-driven fallback. It is not the routine production update path. For
production, prefer public artifacts because they avoid host-local GitHub
credentials and avoid fragile `.git` ownership or macOS metadata drift.

## Local development

### One-command dev boot

```bash
./scripts/dev-up.sh            # start if not already healthy
./scripts/dev-up.sh --status
./scripts/dev-up.sh --rebuild  # rebuild binary then start
./scripts/dev-up.sh --stop
./scripts/dev-up.sh --fg       # foreground
```

Loads `~/.secrets/guda-gateway.env` when present, uses the persistent pair
`~/.local/share/guda-gateway/{gateway.db,master.key}`, sets
`GUDA_ADMIN_COOKIE_SECURE=false`, builds `./guda-gateway` if missing, and
health-checks `http://127.0.0.1:8080/healthz`. Log: `/tmp/guda-gateway-dev.log`.

There are two local dev path setups. **Do not mix them** - the SQLite DB and
master key file are a pair; if you seed keys with one master key and run the
gateway with another, provider key decryption fails with
`cipher: message authentication failed`.

### Persistent macOS dev (recommended)

Keeps a real dev DB and master key under `~/.local/share/guda-gateway/`,
surviving reboots. This matches the bootstrap settings in
`~/.secrets/guda-gateway.env`:

```bash
set -a
. ~/.secrets/guda-gateway.env   # sets DB_PATH, GUDA_MASTER_KEY_PATH, etc.
set +a
# Expand $HOME if your shell does not expand it from the env file:
DB_PATH="$HOME/.local/share/guda-gateway/gateway.db"
GUDA_MASTER_KEY_PATH="$HOME/.local/share/guda-gateway/master.key"

go run ./cmd/guda-gateway-admin --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" db migrate
go run ./cmd/guda-gateway-admin --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" \
  token init --save-env ~/.secrets/guda-gateway.env
go run ./cmd/guda-gateway-admin --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" gateway-key create --name dev
# Paste provider secrets on stdin when prompted for provider-key add.

GUDA_ADMIN_COOKIE_SECURE=false go run ./cmd/guda-gateway
```

For local dev only, `--save-env` writes or replaces
`GUDA_ADMIN_TOKEN=<gat_...>` in the untracked env file with file mode `0600`.
Use the same flag with `token rotate` after rotating the admin token:

```bash
go run ./cmd/guda-gateway-admin --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" \
  token rotate --save-env ~/.secrets/guda-gateway.env
```

### Throwaway dev (quick start)

Uses `/tmp` paths so you start clean each time, no system directories:

```bash
export ADDR=127.0.0.1:8080
export DB_PATH=/tmp/guda-gateway-dev.db
export GUDA_MASTER_KEY_PATH=/tmp/guda-gateway-master.key

go run ./cmd/guda-gateway-admin --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" db migrate
go run ./cmd/guda-gateway-admin --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" token init
go run ./cmd/guda-gateway-admin --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" gateway-key create --name dev
# Paste provider secrets on stdin when prompted for provider-key add.

go run ./cmd/guda-gateway
```

### Master key rotation

If the master key file is replaced or rotated, all provider keys encrypted
with the old key become undecryptable. Re-seed provider keys with:

```bash
printf '%s' "<provider-key>" | ./guda-gateway-admin \
  --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" \
  provider-key add --provider <provider> --name <name>
```

The React admin UI lives in `web/admin`. During local frontend work, run the Go
server and Vite dev server separately:

```bash
./scripts/dev-up.sh
bun run --cwd web/admin dev
```

For release builds, keep production as one Go runtime by embedding the Vite
output into the binary:

```bash
./scripts/build.sh
```

The build script runs `bun install --frozen-lockfile`, builds `web/admin`, copies
the generated files into `internal/adminweb/assets/dist`, then builds
`guda-gateway` and `guda-gateway-admin`.

Smoke test (replace `<gateway-key>` with the value printed by `gateway-key create`):

```bash
curl -sS -H 'Authorization: Bearer <gateway-key>' http://127.0.0.1:8080/healthz
```

## Admin CLI

Binary: `guda-gateway-admin`. Shared flags (before subcommand):

- `--db` (default `/var/lib/code-guda-gateway/gateway.db`)
- `--master-key` (default `/etc/code-guda-gateway/master.key`)

Subcommands include `db migrate`, `token init|rotate|verify`, `gateway-key`,
`provider-key`, `settings`, `audit`, and `usage` — run with no args for usage.
`token init` and `token rotate` print the raw admin token once; pass
`--save-env ~/.secrets/guda-gateway.env` in local dev to also persist
`GUDA_ADMIN_TOKEN` for agent/browser smoke tests.

## Verify

```bash
gofmt -l .
go test ./...
bun run --cwd web/admin test
bun run --cwd web/admin build
./scripts/build.sh
go test -race ./...
go build ./cmd/guda-gateway
go build ./cmd/guda-gateway-admin
CGO_ENABLED=0 go build ./cmd/guda-gateway
CGO_ENABLED=0 go build ./cmd/guda-gateway-admin
```
