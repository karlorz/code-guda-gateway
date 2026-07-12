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

Admin UI:

| Environment | URL | Notes |
|---|---|---|
| **Local frontend work (HMR)** | `http://127.0.0.1:5173/admin/` | `./scripts/dev-up.sh --ui` — Vite + `/admin/api` proxy |
| Local embedded snapshot | `http://127.0.0.1:8080/admin/` | Last `./scripts/build.sh` embed; **no** hot reload |
| Production (kr01) | `https://search.karldigi.dev/admin` | Embedded SPA in the Go binary |

Login with the admin token from CLI / `~/.secrets/guda-gateway.env` (`GUDA_ADMIN_TOKEN`).


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
# Canonical: each row is an atomic (base_url, key) endpoint pair (key via stdin).
guda-gateway-admin provider-endpoint add --provider grok --name primary \
  --base-url https://api.x.ai/v1
guda-gateway-admin provider-endpoint add --provider tavily --name primary \
  --base-url https://api.tavily.com
guda-gateway-admin provider-endpoint add --provider firecrawl --name primary \
  --base-url https://api.firecrawl.dev/v2
# Optional: change provider *defaults* for future creates via settings CLI or admin UI.
# Existing rows keep their own base_url; defaults never re-route live traffic.

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
printf '%s' "$GROK_UPSTREAM_API_KEY" | "$ADM" --db "$DB" --master-key "$MK" \
  provider-endpoint add --provider grok --name primary --base-url https://api.x.ai/v1
printf '%s' "$TAVILY_API_KEY" | "$ADM" --db "$DB" --master-key "$MK" \
  provider-endpoint add --provider tavily --name primary --base-url https://api.tavily.com
printf '%s' "$FIRECRAWL_API_KEY" | "$ADM" --db "$DB" --master-key "$MK" \
  provider-endpoint add --provider firecrawl --name primary --base-url https://api.firecrawl.dev/v2
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

## Container images (optional)

Multi-arch images (`linux/amd64`, `linux/arm64`) are published on release tags
(`v*.*.*`, including current `v0.3.x-stable` tags) and via workflow dispatch.

**This is an optional distribution channel.** Production on `kr01` remains the
public binary installer + systemd + Caddy path above.

### Pull

```bash
# Docker Hub
docker pull karlorz/code-guda-gateway:latest
docker pull karlorz/code-guda-gateway:v0.3.6-stable

# GHCR
docker pull ghcr.io/karlorz/code-guda-gateway:latest
docker pull ghcr.io/karlorz/code-guda-gateway:v0.3.6-stable
```

Image tag equals the full git tag name (e.g. `v0.3.6-stable`), not a stripped
`v0.3.6`.

### Run (dev / experimental)

The image defaults `ADDR=0.0.0.0:8080` so published ports work. Host/systemd
binaries still default to `127.0.0.1:8080`.

```bash
mkdir -p /tmp/guda-docker/{var,etc}
docker run --rm \
  -v /tmp/guda-docker/var:/var/lib/code-guda-gateway \
  -v /tmp/guda-docker/etc:/etc/code-guda-gateway \
  --entrypoint guda-gateway-admin \
  karlorz/code-guda-gateway:latest \
  db migrate

docker run --rm -p 8080:8080 \
  -v /tmp/guda-docker/var:/var/lib/code-guda-gateway \
  -v /tmp/guda-docker/etc:/etc/code-guda-gateway \
  -e GUDA_ADMIN_COOKIE_SECURE=false \
  karlorz/code-guda-gateway:latest
```

Bootstrap env vars are the same as process config: `ADDR`, `DB_PATH`,
`GUDA_MASTER_KEY_PATH`, `GUDA_ADMIN_COOKIE_SECURE`, optional
`GUDA_PROXY_DEBUG_ATTEMPTS`. Do not bake secrets into the image.

### Maintainer: first publish prerequisites

Repo secrets (names only; set with `gh secret set`, never commit values):

| Secret | Purpose |
|--------|---------|
| `DOCKERHUB_USERNAME` | Docker Hub login |
| `DOCKERHUB_TOKEN` | Docker Hub access token |

GHCR uses `GITHUB_TOKEN` with workflow `packages: write`. After secrets exist,
run Actions → **docker-image** → **Run workflow** with an existing tag, or push
the next release tag.

## Local development

### One-command dev boot

```bash
./scripts/dev-up.sh            # start API if not already healthy
./scripts/dev-up.sh --ui       # API + Vite admin HMR (preferred for web/admin work)
./scripts/dev-up.sh --ui-only  # Vite only (API must already be healthy)
./scripts/dev-up.sh --status
./scripts/dev-up.sh --rebuild  # rebuild Go binary then start
./scripts/dev-up.sh --stop     # stop gateway and Vite started by dev-up
./scripts/dev-up.sh --fg       # gateway foreground (no Vite)
```

Loads `~/.secrets/guda-gateway.env` when present, uses the persistent pair
`~/.local/share/guda-gateway/{gateway.db,master.key}`, sets
`GUDA_ADMIN_COOKIE_SECURE=false`, builds `./guda-gateway` if missing, and
health-checks `http://127.0.0.1:8080/healthz`. Logs:
`/tmp/guda-gateway-dev.log`, `/tmp/guda-gateway-vite.log`.

**Admin UI during frontend work:** open **`http://127.0.0.1:5173/admin/`**
(from `--ui`). That is the correct local UI with Vite HMR. Vite proxies
`/admin/api/*` to the gateway so session cookies (`Path=/admin`) and CSRF stay
on the Vite origin.

| URL | Use |
|---|---|
| `http://127.0.0.1:5173/admin/` | Day-to-day admin UI + React HMR |
| `http://127.0.0.1:5173/admin/providers` | Provider Monitoring (pool + quota) |
| `http://127.0.0.1:8080/admin/` | Embedded SPA snapshot only (no HMR) |
| `http://127.0.0.1:8080/healthz` | Gateway health |

`--rebuild` recompiles the **Go** binary (needed for backend/quota parser
changes). It does **not** refresh React sources on `:5173` (Vite already does)
and does **not** refresh the embedded `:8080/admin` SPA (that needs
`./scripts/build.sh`). After Go quota changes, also click **Refresh all quotas**
(or `POST …/provider-key-quotas/{provider}/refresh-all`) so SQLite cache rows
pick up new remaining math.



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
# Paste provider secrets on stdin when prompted for provider-endpoint add
# (requires --base-url; see Provider endpoint pairs).

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
# Paste provider secrets on stdin when prompted for provider-endpoint add
# (requires --base-url; see Provider endpoint pairs).

go run ./cmd/guda-gateway
```

### Master key rotation

If the master key file is replaced or rotated, all provider keys encrypted
with the old key become undecryptable. Re-seed endpoint pairs with:

```bash
printf '%s' "<provider-key>" | ./guda-gateway-admin \
  --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" \
  provider-endpoint add --provider <provider> --name <name> --base-url <url>
```

Or use `./scripts/seed-provider-keys.sh` after exporting env secrets from
`~/.secrets/guda-gateway.env` (template: `scripts/templates/secrets.env.example`).
It creates canonical endpoint pairs with quota sidecars:

| Name | Upstream | Quota |
|---|---|---|
| `grok-1` | `https://new.karldigi.dev/v1` (or `GROK_1_BASE_URL`) | `separate_credentials` → `https://grok.karldigi.dev` + admin key |
| `tavily-1..N` | official / `TAVILY_BASE_URL` | `endpoint_credentials` |
| `firecrawl-1` | official / `FIRECRAWL_BASE_URL` | `endpoint_credentials` |

Keys stay on stdin / `--quota-key-file` only. Keep `GUDA_ADMIN_TOKEN` and
`GUDA_API_KEY` in the secrets file for daily use (`token … --save-env`).

The React admin UI lives in `web/admin`. For local frontend work with HMR:

```bash
./scripts/dev-up.sh --ui
# → http://127.0.0.1:5173/admin/  (Vite + API proxy)
# → http://127.0.0.1:8080/healthz (gateway)
```

Equivalent manual split (same as `--ui`):

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

## Provider endpoint pairs

Every upstream credential is an **atomic endpoint pair**: one row owns both its
`base_url` and encrypted API key. Runtime selection (`SelectEndpoint`) returns
that pair together so retries never cross-wire key A to URL B.

**Endpoint names are identifiers only** — they do not establish primary/backup
roles or pool priority. Selection is sticky-winner + failure demotion (see
below), ordered by demotion state and row id, never by name.

### Creating pairs

Canonical CLI (key on stdin only; never pass secrets as argv):

```bash
printf '%s' "$RAW_KEY" | guda-gateway-admin provider-endpoint add \
  --provider grok|tavily|firecrawl \
  --name NAME \
  --base-url https://upstream.example/path
```

Canonical admin API: `POST /admin/api/provider-endpoints` with JSON
`{provider, name, base_url, key}`. The raw key is accepted only on create/rotate
and is never returned in list or UI responses.

Other operations:

| Action | CLI | Admin API |
|---|---|---|
| List | `provider-endpoint list` | `GET /admin/api/provider-endpoints` |
| Edit URL | `provider-endpoint set-base-url --id ID --url URL` | `POST .../update-base-url` |
| Rotate key | `provider-endpoint rotate-key --id ID` (stdin) | `POST .../rotate-key` |
| Enable/disable/archive | `provider-endpoint enable\|disable\|archive\|... --id ID` | matching routes |

Changing a row's base URL or rotating its key clears that row's cooldown and
demotion (prior failure state described the old pair) while preserving row ID,
counters, and quota history.

### Provider defaults

`provider_settings.base_url` (and admin **Provider Endpoints** → collapsed
**New endpoint defaults**) is only a **creation default** for new rows (and for
the legacy `provider-key add` compatibility path). Changing a provider default
does **not** mutate existing endpoint rows and does not re-route live traffic.

Compiled defaults when settings are unset:

| Provider | Default base URL |
|---|---|
| grok | `https://api.x.ai/v1` |
| tavily | `https://api.tavily.com` |
| firecrawl | `https://api.firecrawl.dev/v2` |

Shared-base seeding (`scripts/seed-provider-keys.sh`) passes an explicit
`--base-url` on every create (env override `GROK_BASE_URL` /
`TAVILY_BASE_URL` / `FIRECRAWL_BASE_URL`, else the compiled default). Keys are
piped on stdin; heterogeneous multi-URL setups are repeated CLI calls, not
parallel URL/key arrays.

### Migration 0008

Migration `0008` adds `provider_keys.base_url TEXT NOT NULL DEFAULT ''` and
backfills each existing row from its provider's configured default, falling
back to the compiled default. Row IDs and encrypted key material are unchanged.
After migration, every row is a self-contained endpoint pair.

### URL validation

`NormalizeBaseURL` rejects:

- non-`http`/`https` schemes
- missing host
- embedded userinfo (credentials in the URL)
- query strings
- fragments

Trailing slashes are stripped; configured path prefixes are preserved.

### Compatibility aliases (`v0.4.x`)

Canonical surfaces land in the `v0.4.0` line:

- CLI: `provider-endpoint …`
- API: `/admin/api/provider-endpoints…`
- Admin UI: **Provider Endpoints** (defaults labeled separately)

Key-named aliases remain through **`v0.4.x`** and are not removed before
**`v0.5.0`**:

- CLI: `provider-key add|list|…` (add snapshots the provider default base URL)
- API: `/admin/api/provider-keys…` (create without `base_url` uses the default)
- Stable row IDs and compatibility JSON identifiers

Compatibility mutations delegate to the same service as the canonical commands.

## Endpoint quota sidecars

Each endpoint row also owns an optional **quota sidecar** — configuration for
how (or whether) plan/credit usage is refreshed for that row. Inference routing
and quota refresh are independent:

```text
Inference: SelectEndpoint(provider) -> row base_url + inference key
           -> cooldown / last_failed_at demotion on cool-policy failures

Quota:     ResolveEndpointQuota(id) -> mode/flow + credentials
           -> updates provider_key_quota_cache only
```

**Quota failures never cool, demote, disable, or reorder inference endpoints.**
Inference failures never erase quota configuration or cache history.

### Quota modes and flows

| Mode | Credentials used | When to use |
|---|---|---|
| `disabled` | none | No quota refresh (Grok create default) |
| `endpoint_credentials` | row inference `base_url` + key | Tavily/Firecrawl create default |
| `separate_credentials` | `quota_base_url` + encrypted quota key | Grok via New API inference + owning Grok2API admin |

| Flow | Provider |
|---|---|
| `grok2api_admin` | grok |
| `tavily_usage` | tavily |
| `firecrawl_credit_usage` | firecrawl |

Creation defaults (migration `0009` backfill matches these):

| Provider | Quota mode | Quota flow |
|---|---|---|
| Grok | `disabled` | `grok2api_admin` |
| Tavily | `endpoint_credentials` | `tavily_usage` |
| Firecrawl | `endpoint_credentials` | `firecrawl_credit_usage` |

Grok often routes **inference** through a New API URL/token while **quota** must
hit the matching Grok2API admin URL and admin key for that deployment. Use
`separate_credentials` so each Grok endpoint row keeps its own quota URL/key and
two rows cannot cross-use credentials.

Tavily and Firecrawl normally reuse the same URL and key used for inference
(`endpoint_credentials`).

### Configuring quota (CLI)

```bash
# Create Grok with separate quota (inference key on stdin; quota key via file or
# second prompt — never as argv):
printf '%s' "$INF_KEY" | guda-gateway-admin provider-endpoint add \
  --provider grok --name new-api-sg \
  --base-url https://new-api.example/v1 \
  --quota-mode separate_credentials \
  --quota-flow grok2api_admin \
  --quota-base-url https://grok2api.example \
  --quota-key-file /path/to/quota.key

# Change mode/flow/URL without rotating secrets:
guda-gateway-admin provider-endpoint set-quota \
  --id ID --mode separate_credentials --flow grok2api_admin \
  --base-url https://grok2api.example

# Rotate only the separate quota key (stdin):
printf '%s' "$QUOTA_KEY" | guda-gateway-admin provider-endpoint rotate-quota-key --id ID

# List includes safe quota metadata only (mode, flow, URL, configured flag, prefix):
guda-gateway-admin provider-endpoint list
```

Admin API:

| Action | Route |
|---|---|
| Create with nested `quota` | `POST /admin/api/provider-endpoints` |
| Update mode/flow/URL | `POST /admin/api/provider-endpoints/{id}/update-quota` |
| Rotate separate quota key | `POST /admin/api/provider-endpoints/{id}/rotate-quota-key` |

List responses expose only safe fields: mode, flow, normalized quota URL, whether
a separate key is configured, and prefix/fingerprint. Raw and encrypted keys are
never returned.

Switching away from `separate_credentials` deletes quota ciphertext and identity
metadata. Switching back requires entering a new quota key.

### Admin UI: configuration vs monitoring

| Page | Route | Responsibility |
|---|---|---|
| **Provider Endpoints** | `/admin/provider-keys` | Create/edit endpoints, inference URL/key, quota mode/flow/URL, rotate keys, enable/disable/archive/delete, creation defaults |
| **Provider Monitoring** | `/admin/providers` | Health tests, pool order/cooldown, inference vs quota status, refresh-one/refresh-all, Promote/Demote/Reset — **no** URL/credential/lifecycle editors |

Quota operational vocabulary on monitoring: `disabled`, `not_configured`,
`not_refreshed`, `available` / `ok`, or `refresh_failed` / `unavailable`.
Refresh-all skips disabled quota sidecars and reports refreshed, failed, and
skipped-disabled counts.

**Pool “Known remaining”** sums `quota.remaining` for **available** endpoints
only (not cooling/disabled/archived). Account-scoped remaining
(`details.remaining_basis=account_plan`, common for Tavily when `key.limit` is
missing) is counted **once** per provider, not once per key. Provider parsers:

| Provider | Remaining source |
|---|---|
| Tavily | Derived: prefer `key.limit − key.usage` (`remaining_basis=key`, additive); if `key.limit` is missing, use `account.plan_limit − account.plan_usage` with matching plan used/limit (`remaining_basis=account_plan`, de-duplicated in Known remaining). No direct remaining field. |
| Firecrawl | Direct `remainingCredits` (with plan/one-time edge cases) |
| Grok (Grok2API admin) | Sum of token mode `remaining` |

**Pool list API:** `GET /admin/api/provider-pools/{provider}?view=enabled|all`
(default `enabled` = selection-eligible rows only; `page.total` is filtered;
summary always reflects the full key set). UI chips: **Active pool** /
**All endpoints**.

If rows show **used N** but the pool has no Known remaining, refresh after a
binary that includes the Tavily fallback, or the upstream response has no
usable limit. Vite HMR does not recompile Go or rewrite the quota cache.

### Admin display timezone

Admin Settings (`/admin/settings`) includes a **display timezone** used only when
rendering audit (and other admin log) timestamps in the UI.

- Storage remains UTC RFC3339Nano in SQLite.
- Default when unset: host process timezone (`time.Local`).
- API: `GET|PATCH /admin/api/settings/display-timezone`
  (`timezone` IANA name; `use_host: true` clears stored value).

### Legacy global Grok quota (`v0.4.x` compatibility)

Provider-global Grok quota settings (`grok set-quota-mode`,
`grok set-admin-base-url`, `grok set-admin-key`, and related admin APIs) remain
readable and mutable through **`v0.4.x`** but are **deprecated**. They are
**not** assigned to endpoint rows by migration `0009` and are **not** used by
canonical per-endpoint quota refresh. Prefer `provider-endpoint set-quota` /
`rotate-quota-key` (or the Provider Endpoints UI). Removal is not before
**`v0.5.0`**.

### Migration 0009

Adds per-row `quota_mode`, `quota_flow`, `quota_base_url`, `encrypted_quota_key`,
`quota_key_prefix`, and `quota_key_fingerprint`. Backfill sets provider defaults
above; no row receives the legacy global Grok2API admin URL or key. Inference
columns, row IDs, cooldowns, and demotion state are unchanged.

## Provider endpoint selection and cooldown

Runtime selection is **sticky winner + failure demotion**, not classic
round-robin:

1. Eligible endpoints: enabled, not archived, not actively cooling
   (`cooldown_until` null or past).
2. Order: never-failed first (`last_failed_at IS NULL`), then oldest
   `last_failed_at`, then lowest `id`.
3. Cool-policy failures (429, Tavily plan-limit **432**, 5xx/408, 401/403,
   network) set cooldown **and** `last_failed_at = now` so the pair sorts to
   the end after cool expires.
4. Success clears demotion (`last_failed_at = NULL`).
5. Defaults: rate/plan cool **60s**, transient **30s**, credential **1h**,
   `max_retries` **3** (overridable via SQLite settings).

Quota cache state is **not** an input to selection. A row with zero remaining
plan quota may still be selected for inference; operators use monitoring refresh
and pool demotion/cooldown tools as needed.

Tavily upstream **432** is stored as key health `plan_limit_exceeded` and
mapped to client **429** `tavily_plan_limit_exceeded` only if all attempts fail.

### Admin pool controls

On **Provider Monitoring** (per-provider pool table) and **Provider Endpoints**:

| Action | Effect |
|---|---|
| **Reset** / **Reset cool+order** | Clears cooldown and demotion |
| **Promote** | Clears demotion only (`last_failed_at`) |
| **Demote** | Sets `last_failed_at=now` (no cooldown) |
| **Order** column | `front pack` vs `demoted · <time>` |

CLI (canonical; `provider-key` aliases work the same through `v0.4.x`):

```bash
guda-gateway-admin provider-endpoint reset-cooldown --id ID
guda-gateway-admin provider-endpoint reset-selection --id ID
guda-gateway-admin provider-endpoint demote --id ID
```

Pool STATUS `available` means enabled + not cooling + has a quota row. It does
**not** mean remaining plan quota is non-zero; demotion/cooldown handle routing.

## Admin CLI

Binary: `guda-gateway-admin`. Shared flags (before subcommand):

- `--db` (default `/var/lib/code-guda-gateway/gateway.db`)
- `--master-key` (default `/etc/code-guda-gateway/master.key`)

Subcommands include `db migrate`, `token init|rotate|verify`, `gateway-key`,
`provider-endpoint` (canonical: add with `--base-url` and optional quota flags,
list, set-base-url, rotate-key, set-quota, rotate-quota-key, reset-cooldown,
reset-selection, demote, …),
`provider-key` (compatibility alias through `v0.4.x`),
`grok` (legacy global Grok settings; global quota subcommands deprecated through
`v0.4.x`),
`settings`, `audit`, and `usage` — run with no args for usage.
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
