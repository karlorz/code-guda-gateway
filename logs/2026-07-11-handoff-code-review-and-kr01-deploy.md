# Handoff: full codebase review → fix → final kr01 deploy

**Date:** 2026-07-11  
**Repo:** `/Users/karlchow/Desktop/code/code-guda-gateway`  
**Branch:** `main` @ `ed926a4` (= `origin/main`)  
**Stable live tag (pre this work):** `v0.3.2-stable` on kr01 `search.karldigi.dev`  
**Do not put secret values in this file, commits, wiki, or chat logs.**

---

## Mission for the next session

1. **Full codebase code-review** of endpoint pairs + quota sidecars + recent ops fixes on `main`.
2. **Fix** any review findings (TDD; keep public facade contract).
3. **Final deploy + seed on kr01** only after review is clean and user confirms.
4. **Do not** rotate tokens/keys unless user says **force**.
5. **Do not** tag/release until review + deploy path are agreed.

Suggested skills order:

1. `superpowers:using-superpowers`
2. `skillwiki:using-skillwiki` (vault paths only; no secrets)
3. `superpowers:requesting-code-review` / `code-review` skill on `v0.3.2-stable..main` or `4c9ff5c^..HEAD`
4. `simplify:simplify` after review fixes
5. `superpowers:verification-before-completion`
6. Deploy only with explicit user go-ahead (installer is public curl|bash path)

---

## What shipped (already on main)

### Product features

| Area | State |
|------|--------|
| Provider **endpoint pairs** | Each `provider_keys` row owns `base_url` + encrypted inference key; `SelectEndpoint` sticky-winner + demotion |
| Provider **quota sidecars** | Migration `0009`: `quota_mode` / `quota_flow` / optional `quota_base_url` + encrypted quota key |
| Admin UI split | **Provider Endpoints** (`/admin/provider-keys`) configures; **Provider Monitoring** (`/admin/providers`) observes/ops |
| CLI | `provider-endpoint` canonical (`add`, `set-quota`, `rotate-quota-key`, …); `provider-key` + global `grok set-admin-*` deprecated through **v0.4.x** |
| PR | https://github.com/karlorz/code-guda-gateway/pull/4 squash-merged as `4c9ff5c` |

### Follow-up commits after PR #4

| Commit | Summary |
|--------|---------|
| `b4829d9` | Secrets template + seed script aligned to canonical topology |
| `ed926a4` | Unknown paths **404** before gateway auth (favicon was 401); seed idempotent/quiet; legacy name probes behind `SEED_LEGACY_NAMES=1` |

### Work items (vault)

All under `projects/code-guda-gateway/work/` are **`status: completed`**, including:

- `2026-07-11-provider-endpoint-pairs`
- `2026-07-11-provider-endpoint-quota-sidecars`

---

## Canonical production topology (names are labels only)

| Name | Provider | Inference URL | Quota |
|------|----------|---------------|--------|
| `grok-1` | grok | `https://new.karldigi.dev/v1` (New API) | **`separate_credentials`** → `https://grok.karldigi.dev` + Grok2API admin key |
| `tavily-1..3` | tavily | `https://api.tavily.com` | `endpoint_credentials` / `tavily_usage` |
| `firecrawl-1` | firecrawl | `https://api.firecrawl.dev/v2` | `endpoint_credentials` / `firecrawl_credit_usage` |

**Critical:** New API ≠ Grok2API. Inference and quota hosts must not be conflated.  
Selection ignores quota cache; quota failures must never demote/cool inference rows.

---

## Secrets / config hygiene (done on this machine)

### Local secrets file

Path: `~/.secrets/guda-gateway.env` (mode `0600`, **untracked**)

**Cleaned 2026-07-11** — removed stale:

- `GUDA_GATEWAY_KEYS`
- `GROK_UPSTREAM_*`
- `GROK_API_URL` / `TAVILY_API_URL` / `FIRECRAWL_API_URL`
- global `grok_quota_mode`
- singular-only `TAVILY_API_KEY` (multi list kept)

**Canonical keys remaining:** bootstrap + daily auth + `GROK_1_*` / `TAVILY_*` / `FIRECRAWL_*`  
**Seed aliases still present (same values):** `GROK_BASE_URL`, `GROK_API_KEY`, `grok2api_admin_*`, `FIRECRAWL_API_KEY`

Backup: `~/.secrets/guda-gateway.env.bak-clean-20260711-191237`

### Templates (in repo)

| File | Role |
|------|------|
| `scripts/templates/bootstrap.env.example` | **Prod process-only** (no secrets) |
| `scripts/templates/secrets.env.example` | **Canonical seed/auth template** (placeholders only) |
| `scripts/seed-provider-keys.sh` | Seeds endpoint pairs + Grok separate quota; quiet re-runs |

### MCP (local agents → local gateway)

All GuDa-facing MCP currently point at **local** `http://127.0.0.1:8080` + current local `gsk_`:

- Project: `.mcp.json` (gitignored), `.codex/config.toml`
- Global: `~/.codex/config.toml` `[mcp_servers.grok-search]`, `~/.claude.json` `mcpServers.grok-search`

**After prod is live**, switch agent MCP to:

- `GUDA_BASE_URL=https://search.karldigi.dev`
- **prod** gateway key (`gsk_…` created on kr01), not the macOS dev key

**Not GuDa:** `~/.agents/mcp.json` still uses **direct** Grok/Tavily (`clifree…`) — leave or migrate intentionally.

---

## Local dev runtime (reference)

| Item | Value |
|------|--------|
| Dev-up | `./scripts/dev-up.sh` / `--status` / `--stop` / `--rebuild` |
| Bootstrap paths | `DB_PATH=$HOME/.local/share/guda-gateway/gateway.db`, master key beside it |
| Admin UI | `http://127.0.0.1:8080/admin` (JSON login SPA) |
| Facade smoke | `GET /grok/v1/models` with `Authorization: Bearer $GUDA_API_KEY` → 200 |
| Unknown paths | `/favicon.ico`, bare `/v1/models` → **404** (not 401) |
| Last full reinit | Fresh DB + seed; active rows only the 5 canonical names |

**Policy:** Do not refresh `GUDA_ADMIN_TOKEN` / `GUDA_API_KEY` unless user says **force**. Empty-DB reinit necessarily creates new tokens.

After `web/admin` changes: `./scripts/build.sh` then restart — `dev-up --rebuild` alone does **not** refresh SPA embed.

---

## Verification already green on `ed926a4`

```bash
gofmt -l .                    # clean
go test ./...                 # PASS
go test -race ./internal/server ./internal/providers ./internal/adminweb ./internal/store ./cmd/guda-gateway-admin
bun run --cwd web/admin test --run   # 42
./scripts/build.sh
git diff --check
```

Live: healthz 200; grok models auth 200; favicon 404.

---

## Recommended review scope

Diff basis: **`v0.3.2-stable..main`** or at least **`a3ee908..ed926a4`** focusing on:

- `internal/providers/` (keys, endpoint_quota, quota_refresh, selection)
- `internal/proxy/`, `internal/server/` (auth order / isRuntimeRoute)
- `internal/adminweb/`, `web/admin/src/`
- `cmd/guda-gateway-admin/`
- `internal/store/migrations.go` (0008, 0009)
- `scripts/seed-provider-keys.sh`, templates, README

### Known non-blocking / review notes

1. `SkippedDisabled` may mix inference-disabled + `quota_mode=disabled` (same counter).
2. SPA login is JSON-only; HTML form login works with named `token` field (302) — harness confusion only.
3. Seed re-run is quiet; legacy name patch only if `SEED_LEGACY_NAMES=1`.
4. Doctor-worker may false-flag if it looks for `skills/dev-loop/dependencies.yaml` under **repo** instead of plugin cache.
5. Compatibility: keep `provider-key` / global Grok quota through v0.4.x; remove not before v0.5.0.

### Security review focus

- No raw/encrypted keys in list/UI/audit/logs
- Separate encrypt for inference vs quota keys
- Quota path never mutates `last_failed_at` / cooldown / enable / archive
- Create/rotate only accept secrets; never return them

---

## Prod deploy / seed plan (kr01) — after review

**Host:** kr01 · public site `search.karldigi.dev`  
**Installer:** `curl -fsSL https://raw.githubusercontent.com/karlorz/code-guda-gateway/main/deploy/code-guda-gateway/install.sh | bash`  
**Constraint:** repo must stay public for no-token-on-kr01 path.

### Bootstrap on host (process only)

```text
ADDR=127.0.0.1:8080   # or host convention
DB_PATH=/var/lib/code-guda-gateway/gateway.db
GUDA_MASTER_KEY_PATH=/etc/code-guda-gateway/master.key
GUDA_ADMIN_COOKIE_SECURE=true
```

**Never** copy macOS `$HOME/.local/share/...` paths to prod.

### Seed (operator secrets, not in git)

1. `db migrate` (0008 pairs + 0009 sidecars)
2. `token init` / `token rotate` — capture `gat_` once into **prod** secret store (not dev env)
3. `gateway-key create` — capture `gsk_` for MCP/prod clients
4. `./scripts/seed-provider-keys.sh` with **prod** secrets file matching topology above
5. `provider-endpoint list` — expect 5 active rows; grok-1 `separate_credentials` + `quota_key_configured=true`
6. Smoke: public health + `/grok/v1/models` with **prod** key
7. Point agent MCP at `https://search.karldigi.dev` + prod `gsk_`

If master key is new/rotated, re-seed all endpoint + quota secrets (old ciphertext unreadable).

### Tag / release

Only after review + successful kr01 verify. Current stable is still **`v0.3.2-stable`** until a new tag is cut intentionally.

---

## Suggested first commands next session

```bash
cd /Users/karlchow/Desktop/code/code-guda-gateway
git fetch origin && git status -sb && git log -5 --oneline
./scripts/dev-up.sh --status
# Review:
git diff --stat v0.3.2-stable..HEAD
# or: code-review / simplify on that range
go test ./...
bun run --cwd web/admin test --run
```

Read also:

- `CLAUDE.md` (runtime notes)
- `README.md` (endpoint pairs + quota sidecars)
- `scripts/templates/secrets.env.example`
- Vault: `projects/code-guda-gateway/requirements/2026-07-09-dev-ops-env-and-provider-key-seeding.md`
- Specs: `work/2026-07-11-provider-endpoint-pairs/`, `work/2026-07-11-provider-endpoint-quota-sidecars/`

---

## Out of scope unless asked

- Rotating live provider/admin/gateway secrets without **force**
- Changing kr01 before review sign-off
- Removing v0.4.x compatibility aliases early
- Committing `~/.secrets/*` or real keys into the repo

---

## One-line status

**Code complete on `main` (`ed926a4`); local secrets/MCP cleaned; all work items completed; next session = full review → fix → user-approved kr01 deploy/seed.**
