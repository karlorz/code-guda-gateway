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
in environment variables. The service only needs these bootstrap settings (env
or `/etc/code-guda-gateway/bootstrap.env`):

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `127.0.0.1:8080` | Listen address (localhost by default). |
| `DB_PATH` | `/var/lib/code-guda-gateway/gateway.db` | SQLite database path. |
| `GUDA_MASTER_KEY_PATH` | `/etc/code-guda-gateway/master.key` | File used to load or create the encryption master key. |

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

Use temporary paths so you do not need system directories:

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

## Verify

```bash
gofmt -l .
go test ./...
go test -race ./...
go build ./cmd/guda-gateway
go build ./cmd/guda-gateway-admin
CGO_ENABLED=0 go build ./cmd/guda-gateway
CGO_ENABLED=0 go build ./cmd/guda-gateway-admin
```