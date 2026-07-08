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

## Local development

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
GUDA_ADMIN_COOKIE_SECURE=false ADDR=127.0.0.1:8080 go run ./cmd/guda-gateway
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
