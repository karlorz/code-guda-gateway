# CLAUDE.md

> Project doc index. Paths use placeholders: `{WIKI_PATH}` (`/Users/karlchow/wiki`),
> `{HOME}`, `{REPO_ROOT}`. No secret values.

## Vault

- Project: `{WIKI_PATH}/projects/code-guda-gateway/README.md`
- Knowledge: `{WIKI_PATH}/projects/code-guda-gateway/knowledge.md`
- Dev ops (env paths, multi-key seeding): `{WIKI_PATH}/projects/code-guda-gateway/requirements/2026-07-09-dev-ops-env-and-provider-key-seeding.md`
- Work items: `{WIKI_PATH}/projects/code-guda-gateway/work/`
- Latest completed: Provider key failure demotion (2026-07-10) under
  `{WIKI_PATH}/projects/code-guda-gateway/work/2026-07-10-provider-key-failure-demotion/`
- Concept: `{WIKI_PATH}/concepts/guda-gateway-provider-key-failure-demotion.md`

## Repo

- README: `{REPO_ROOT}/README.md` (contract, env, dev/prod, key selection)
- Dev boot: `{REPO_ROOT}/scripts/dev-up.sh` (API only; UI embed needs `{REPO_ROOT}/scripts/build.sh`)
- Handoffs: `{REPO_ROOT}/logs/` (latency note: `2026-07-10-tavily-cooldown-latency.md`)
- Build: `{REPO_ROOT}/scripts/build.sh` (admin SPA embed + both binaries; uses `-buildvcs=false`)
- Bootstrap template: `{REPO_ROOT}/scripts/templates/bootstrap.env.example`
- Current stable release: `v0.3.2-stable` (tag on `main`, live on kr01 `search.karldigi.dev`)
- Public installer: `curl -fsSL https://raw.githubusercontent.com/karlorz/code-guda-gateway/main/deploy/code-guda-gateway/install.sh | bash`
  (repo must be public for the no-token-on-kr01 deploy path)

## Runtime notes (no secrets)

- Provider `SelectKey`: sticky winner + `last_failed_at` demotion + cooldown skip.
- Admin pool Order column + Promote/Demote/Reset; CLI `reset-selection` / `demote`.
- After `web/admin` changes: `./scripts/build.sh` then restart; `dev-up --rebuild` alone does not refresh SPA.
