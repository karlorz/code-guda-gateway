# CLAUDE.md

> Project **doc index** only. Placeholders: `{WIKI_PATH}` (`/Users/karlchow/wiki`),
> `{HOME}`, `{REPO_ROOT}` (this repo). No secret values.

## Rule

- This file is a **link index**, not operational notes.
- Prefer **updating a path here** or **editing the linked README/wiki page**.
- Do **not** add runtime behavior, gotchas, or how-to prose to this file.
- Put product/ops detail in the linked docs; keep this file short.

## Docs index

### Vault (`{WIKI_PATH}/projects/code-guda-gateway/`)

| | Path |
|--|------|
| Project | `{WIKI_PATH}/projects/code-guda-gateway/README.md` |
| Knowledge | `{WIKI_PATH}/projects/code-guda-gateway/knowledge.md` |
| Dev ops / env / seeding / local admin URLs | `{WIKI_PATH}/projects/code-guda-gateway/requirements/2026-07-09-dev-ops-env-and-provider-key-seeding.md` |
| Roadmap | `{WIKI_PATH}/projects/code-guda-gateway/requirements/2026-07-07-robust-management-deployment-roadmap.md` |
| Work items | `{WIKI_PATH}/projects/code-guda-gateway/work/` |
| Latest completed (web admin console) | `{WIKI_PATH}/projects/code-guda-gateway/work/2026-07-11-web-admin-ui-improvement/` |
| Endpoint pairs | `{WIKI_PATH}/projects/code-guda-gateway/work/2026-07-11-provider-endpoint-pairs/` |
| Quota sidecars | `{WIKI_PATH}/projects/code-guda-gateway/work/2026-07-11-provider-endpoint-quota-sidecars/` |
| Failure demotion | `{WIKI_PATH}/projects/code-guda-gateway/work/2026-07-10-provider-key-failure-demotion/` |
| Concept: key demotion | `{WIKI_PATH}/concepts/guda-gateway-provider-key-failure-demotion.md` |

### Repo (`{REPO_ROOT}`)

| | Path |
|--|------|
| README (contract, env, admin URLs, pairs, quota, selection) | `{REPO_ROOT}/README.md` |
| Dev boot | `{REPO_ROOT}/scripts/dev-up.sh` |
| Build / SPA embed | `{REPO_ROOT}/scripts/build.sh` |
| Seed endpoints | `{REPO_ROOT}/scripts/seed-provider-keys.sh` |
| Bootstrap template | `{REPO_ROOT}/scripts/templates/bootstrap.env.example` |
| Secrets template | `{REPO_ROOT}/scripts/templates/secrets.env.example` |
| Handoffs | `{REPO_ROOT}/logs/` |
| Admin SPA | `{REPO_ROOT}/web/admin/` |
| Public installer | `https://raw.githubusercontent.com/karlorz/code-guda-gateway/main/deploy/code-guda-gateway/install.sh` |
