# Instance seed flow (VM · Coolify · local)

One coverage path for every host. **Coolify is optional**; the same script
runs on kr01, a laptop, or a container.

## Coverage steps (required)

| Step | What | Idempotent? |
|------|------|-------------|
| 1 | `db migrate` | yes |
| 2 | Admin token (`sync-env` if `GUDA_ADMIN_TOKEN` set, else `init` once) | yes |
| 3 | Gateway key `create --name …` if name missing; raw → save-env | yes on name |
| 4 | Provider endpoints + quota via `seed-provider-keys.sh` | yes on (provider,name) |

Script: `scripts/seed-instance.sh` (in image as `seed-instance`).

## A) VM / kr01 (canonical)

```bash
# 1. Install binary path (public installer or package)
# 2. Secrets file (never commit) — copy from scripts/templates/secrets.env.example
set -a; . /path/to/guda-gateway.env; set +a
export DB_PATH=/var/lib/code-guda-gateway/gateway.db
export GUDA_MASTER_KEY_PATH=/etc/code-guda-gateway/master.key

./scripts/seed-instance.sh /opt/code-guda-gateway/bin/guda-gateway-admin
# or after PATH has guda-gateway-admin:
GUDA_SEED_PROFILE=prod ./scripts/seed-instance.sh
```

Verify:

```bash
guda-gateway-admin --db "$DB_PATH" --master-key "$GUDA_MASTER_KEY_PATH" provider-endpoint list
# admin UI: GUDA_ADMIN_TOKEN from secrets file
# MCP/API: GUDA_API_KEY from secrets file (after create)
```

## B) Coolify / Docker (optional)

### B1. Gateway only (always)

`docker-compose.coolify-tag.yml`:

- Magic admin: `GUDA_ADMIN_TOKEN=${SERVICE_PASSWORD_64_GUDAADMIN}`
- Entrypoint syncs admin hash + volume mirror
- **Does not** seed providers unless you set `GUDA_SEED_ON_START=1`

### B2. Full seed once (recommended Coolify)

After gateway is healthy, set provider secrets in Coolify Environment (locked),
then either:

```bash
# One-shot on the running gateway container
docker exec \
  -e GUDA_SEED_PROFILE=coolify \
  -e GROK_1_API_KEY=... \
  -e GROK_1_QUOTA_KEY=... \
  -e TAVILY_API_KEYS=... \
  -e FIRECRAWL_1_API_KEY=... \
  code-guda-gateway-<uuid> seed-instance
```

or:

```bash
docker compose -f docker-compose.coolify-tag.yml -f docker-compose.coolify-seed.yml \
  --profile seed run --rm seed
```

### B3. Seed on every start (optional, heavier)

On the gateway service env:

```text
GUDA_SEED_ON_START=1
GUDA_SEED_PROFILE=coolify
# + same provider secret env vars
```

Entrypoint runs `seed-instance` after admin bootstrap (skips re-init admin).

## Env matrix

| Variable | Process runtime | seed-instance | Coolify UI |
|----------|-----------------|---------------|------------|
| `ADDR` / `DB_PATH` / `GUDA_MASTER_KEY_PATH` | yes | needs DB+MK | bootstrap |
| `GUDA_ADMIN_TOKEN` | no (hash in DB) | sync-env | magic password |
| `GUDA_API_KEY` | no (hash in DB) | written on create | optional daily |
| `GROK_*` / `TAVILY_*` / `FIRECRAWL_*` | no | yes | locked secrets |

## Safety

- No secret values in git or wiki
- Provider keys only via stdin / seed-provider-keys (not argv)
- Re-run seed anytime; skips existing endpoint names and gateway-key names
