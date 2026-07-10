# Dev Loop - Code GuDa Gateway

## Identity

```yaml
slug: code-guda-gateway
release_branch: main
```

## PRD Layer

```yaml
prd_layer: superpowers
prd_pipeline: tdd-first
```

## Cross-Cutting Disciplines

```yaml
prd_disciplines:
  - skill: superpowers:test-driven-development
    when: execute
    mode: mandatory
    include_paths:
      - cmd/**
      - internal/**
      - web/admin/src/**
      - scripts/**
  - skill: superpowers:test-driven-development
    when: execute
    mode: advisory
  - skill: superpowers:systematic-debugging
    when: failure
    mode: reactive
```

## Critical Paths

```yaml
critical_paths:
  provider_routing:
    code:
      - internal/providers/**
      - internal/proxy/**
      - internal/server/**
    vault:
      - 2026-07-08-admin-provider-quotas
      - 2026-07-09-multi-key-provider-pool-observability
    history_pins:
      - "2026-07-10: valid per-key quota data conflicted with a stale provider-wide warning"
  admin_control_plane:
    code:
      - internal/adminweb/**
      - web/admin/src/**
    vault:
      - 2026-07-08-admin-ui-v2
      - 2026-07-09-multi-key-provider-pool-observability
    history_pins:
      - "Admin pages must not expose provider keys, tokens, or other secrets."
  production_release:
    code:
      - scripts/**
      - README.md
    vault:
      - 2026-07-09-public-remote-installer-deploy
    history_pins:
      - "kr01 cannot update a private GitHub repository without non-interactive credentials."
```

## Fact Check

```yaml
fact_check:
  enabled: true
  source_order:
    - local_repo
    - context7
    - vault_query
  triggers:
    - "version"
    - "deprecat"
    - "CVE-"
  evidence_contract:
    require_sources_used_section: true
```

## Investigate And Preflight

```yaml
investigate:
  max_items: 5
  topic_seeds:
    - provider quota behavior
    - provider key pool observability
    - production release automation

preflight:
  enabled: true
  default_limit: 5
  default_lanes:
    - work
    - captures
    - hygiene
  require_approved_spec_and_plan: true
  unattended_not_ready_behavior: skip
  defaults:
    compatibility_policy: "Preserve the public gateway contract and admin API compatibility unless a work item explicitly scopes a change."
```

## Reactive Debugging

```yaml
reactive_debugging:
  enabled: true
  auto_retry_attempts: 2
  evidence_dir: .claude/dev-loop-debug/
  evidence_capture:
    - "go test ./... 2>&1 | tee {evidence_dir}/{cycle}-go-test.log"
    - "bun run --cwd web/admin test 2>&1 | tee {evidence_dir}/{cycle}-admin-test.log"
    - "git diff --stat"
    - "git log --oneline -5"
  escalate_after:
    consecutive_idle_cycles: 3
    same_error_signature: true
  escalation_action: surface_p1_finding
```

## Code Review

```yaml
code_review:
  parallel: true
  codex:
    enabled_in_normal: false
    enabled_in_high: true
    agent: dev-loop:codex-review-worker
```

## Knowledge

```yaml
knowledge_layer: skillwiki
knowledge_backends:
  skillwiki:
    vault: auto
    cli_entry: skillwiki
vault_auto_commit: true
vault_sync:
  peer_aware: false
  lock_timeout_seconds: 30
  retry_budget: 3
  presync_skill: auto-detect
```

## Interview

```yaml
interview:
  setup:
    skill: setup-dev-loop
  work_item:
    upgrade: grill-me
    trigger: manual
    goal_override: never
```

## Code Layout

```yaml
cli_src: cmd/
cli_test: ""
skills_glob: ""
cli_entry_override: ""
```

## E2E

```yaml
e2e_scripts: []
```

## Release

```yaml
bump_script: ""
publish_via: none
deploy_script: ""
manifests_count: 0
remote_hosts: []
```

## CI

```yaml
ci_configured: true
ci_discovery: runtime
```

## Notes

```yaml
notes:
  canonical_spec: "projects/code-guda-gateway/work/2026-07-09-multi-key-provider-pool-observability/spec.md"
  compatibility: "The service remains a code.guda.studio-compatible facade for GrokSearch MCP."
  deployment: "Do not automate kr01 deployment until the updater can fetch the private repository non-interactively or a public artifact deploy script exists."
  vault_sync: "Disabled until dev-loop uses SkillWiki sync lock/unlock instead of the obsolete --acquire-lock/--release-lock interface."
```
