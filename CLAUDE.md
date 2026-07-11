# Agent In

> Project doc index. Paths use placeholders: `{WIKI_PATH}` (`/Users/karlchow/wiki`),
> `{HOME}`, `{REPO_ROOT}`. No secret values.

## Vault

- Project: `{WIKI_PATH}/projects/code-guda-gateway/README.md`
- Knowledge: `{WIKI_PATH}/projects/code-guda-gateway/knowledge.md`
- Dev ops (env paths, multi-key seeding, local admin URLs): `{WIKI_PATH}/projects/code-guda-gateway/requirements/2026-07-09-dev-ops-env-and-provider-key-seeding.md`
- Work items: `{WIKI_PATH}/projects/code-guda-gateway/work/`
- Latest completed: Web admin UI improvement / compact operational console (2026-07-12) under
  `{WIKI_PATH}/projects/code-guda-gateway/work/2026-07-11-web-admin-ui-improvement/`;
  also provider endpoint pairs + quota sidecars (2026-07-11)
- Concept: `{WIKI_PATH}/concepts/guda-gateway-provider-key-failure-demotion.md`

## Repo

- README: `{REPO_ROOT}/README.md` (contract, env, **local admin URLs**, endpoint pairs, quota, selection)
- Dev boot:
  - API only: `{REPO_ROOT}/scripts/dev-up.sh`
  - **Admin HMR (preferred):** `{REPO_ROOT}/scripts/dev-up.sh --ui` → open **`http://127.0.0.1:5173/admin/`**
    (Vite proxies `/admin/api` → gateway). Do **not** use `:8080/admin` for day-to-day UI work.
  - Embedded SPA at `http://127.0.0.1:8080/admin/` needs `{REPO_ROOT}/scripts/build.sh` and is **not** HMR.
- Handoffs: `{REPO_ROOT}/logs/`
- Build: `{REPO_ROOT}/scripts/build.sh` (admin SPA embed + both binaries; uses `-buildvcs=false`)
- Bootstrap template: `{REPO_ROOT}/scripts/templates/bootstrap.env.example`
- Seed endpoints: `{REPO_ROOT}/scripts/seed-provider-keys.sh` (`provider-endpoint add --base-url`, key on stdin)
- Current stable release: `v0.3.3-stable` (tag on `main`, live on kr01 `search.karldigi.dev`)
- Public installer: `curl -fsSL https://raw.githubusercontent.com/karlorz/code-guda-gateway/main/deploy/code-guda-gateway/install.sh | bash`
  (repo must be public for the no-token-on-kr01 deploy path)

## Runtime notes (no secrets)

- Endpoint pairs: each `provider_keys` row owns `base_url` + encrypted key; proxy
  uses `SelectEndpoint` (sticky winner + `last_failed_at` demotion + cooldown skip).
- Names are identifiers only — not primary/backup roles.
- Quota sidecars (migration `0009`): per-row `quota_mode` /
  `quota_flow` / optional `quota_base_url` + `encrypted_quota_key`.
  Defaults: Grok `disabled`+`grok2api_admin`; Tavily/Firecrawl
  `endpoint_credentials` + their usage flows. Grok may use New API for
  inference and separate Grok2API admin URL/key for quota.
- Quota refresh never mutates cooldown/demotion/enabled; inference never
  erases quota config/cache.
- **Tavily remaining:** no direct remaining field; derive `key.limit−key.usage`,
  else `account.plan_limit−plan_usage` (`remaining_basis` in details). Pool
  **Known remaining** sums remaining for **available** rows only. After Go
  quota parser changes: `dev-up --rebuild` (+ restart) then **Refresh all quotas**.
- Provider settings `base_url` is a **creation default only** — never a runtime
  fallback; changing it does not mutate existing rows (migration 0008 backfilled
  snapshots).
- Canonical admin: CLI `provider-endpoint` (incl. `set-quota` /
  `rotate-quota-key`) / API `/admin/api/provider-endpoints`.
- Admin UI: **Provider Endpoints** configures; **Provider Monitoring** observes
  and runs refresh/order ops (no settings editor).
- Compatibility through **v0.4.x**: `provider-key` and `/admin/api/provider-keys`
  (legacy add snapshots provider default base URL); legacy global Grok quota
  (`grok set-quota-mode|set-admin-*`) deprecated and not assigned to endpoint
  rows. Not removed before **v0.5.0**.
- Admin pool Order column + Promote/Demote/Reset; CLI `reset-selection` / `demote`.
- URL validation: no userinfo, query, or fragment in base URLs (inference or quota).
- After `web/admin` changes for **release/embed**: `./scripts/build.sh` then restart gateway.
  Day-to-day UI: `./scripts/dev-up.sh --ui` and edit against **`:5173/admin`** (HMR).
  `dev-up --rebuild` alone does not refresh the embedded SPA.
