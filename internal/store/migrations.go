package store

import (
	"database/sql"
	"fmt"
	"time"
)

type migration struct {
	id  string
	sql string
}

var migrations = []migration{
	{
		id:  "0001",
		sql: migration0001,
	},
	{
		id:  "0002",
		sql: migration0002,
	},
	{
		id:  "0003",
		sql: migration0003,
	},
	{
		id:  "0004",
		sql: migration0004,
	},
	{
		id:  "0005",
		sql: migration0005,
	},
	{
		id:  "0006",
		sql: migration0006,
	},
	{
		id:  "0007",
		sql: migration0007,
	},
	{
		id:  "0008",
		sql: migration0008,
	},
	{
		id:  "0009",
		sql: migration0009,
	},
}

const migration0002 = `
ALTER TABLE admin_tokens ADD COLUMN key_prefix TEXT NOT NULL DEFAULT '';
`

const migration0003 = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_keys_provider_name ON provider_keys(provider, name);
`

const migration0004 = `
ALTER TABLE admin_sessions ADD COLUMN csrf_token_hash TEXT NOT NULL DEFAULT '';
`

const migration0005 = `
ALTER TABLE provider_keys ADD COLUMN archived_at TEXT;
ALTER TABLE provider_keys ADD COLUMN last_event_at TEXT;
ALTER TABLE provider_keys ADD COLUMN last_event_source TEXT;
ALTER TABLE provider_keys ADD COLUMN last_event_status_class TEXT;
ALTER TABLE provider_keys ADD COLUMN last_event_http_status INTEGER;
ALTER TABLE provider_keys ADD COLUMN last_event_message_redacted TEXT;

CREATE TABLE IF NOT EXISTS provider_quota_cache (
  provider TEXT NOT NULL PRIMARY KEY,
  provider_key_id INTEGER,
  source TEXT NOT NULL,
  available INTEGER NOT NULL,
  used INTEGER,
  limit_value INTEGER,
  remaining INTEGER,
  period_start TEXT,
  period_end TEXT,
  checked_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  message_redacted TEXT
);
`

const migration0006 = `
CREATE TABLE IF NOT EXISTS provider_key_quota_cache (
  provider_key_id INTEGER NOT NULL PRIMARY KEY,
  provider TEXT NOT NULL,
  source TEXT NOT NULL,
  available INTEGER NOT NULL,
  used INTEGER,
  limit_value INTEGER,
  remaining INTEGER,
  period_start TEXT,
  period_end TEXT,
  checked_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  message_redacted TEXT,
  details_json TEXT
);

CREATE INDEX IF NOT EXISTS idx_provider_key_quota_cache_provider
  ON provider_key_quota_cache(provider);

CREATE TABLE IF NOT EXISTS proxy_attempt_logs (
  id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  occurred_at TEXT NOT NULL,
  request_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  route_family TEXT NOT NULL,
  path TEXT NOT NULL,
  attempt_index INTEGER NOT NULL,
  provider_key_id INTEGER,
  provider_key_name TEXT,
  provider_key_fingerprint TEXT,
  upstream_status INTEGER,
  status_class TEXT NOT NULL,
  reason TEXT,
  cooldown_until TEXT,
  terminal INTEGER NOT NULL,
  message_redacted TEXT
);

CREATE INDEX IF NOT EXISTS idx_proxy_attempt_logs_request_id
  ON proxy_attempt_logs(request_id, id);

CREATE INDEX IF NOT EXISTS idx_proxy_attempt_logs_occurred_at
  ON proxy_attempt_logs(occurred_at, id);
`

// last_failed_at drives sticky-winner failure demotion:
// SelectKey orders never-failed keys first, then oldest failure, then id.
const migration0007 = `
ALTER TABLE provider_keys ADD COLUMN last_failed_at TEXT;
CREATE INDEX IF NOT EXISTS idx_provider_keys_select
  ON provider_keys(provider, enabled, last_failed_at, id);
`

// base_url turns every provider_keys row into an atomic endpoint pair. Existing
// rows snapshot the configured provider default, falling back to compiled URLs.
const migration0008 = `
ALTER TABLE provider_keys ADD COLUMN base_url TEXT NOT NULL DEFAULT '';

UPDATE provider_keys
SET base_url = COALESCE(
  (SELECT NULLIF(TRIM(provider_settings.base_url), '')
   FROM provider_settings
   WHERE provider_settings.provider = provider_keys.provider),
  CASE provider
    WHEN 'grok' THEN 'https://api.x.ai/v1'
    WHEN 'tavily' THEN 'https://api.tavily.com'
    WHEN 'firecrawl' THEN 'https://api.firecrawl.dev/v2'
    ELSE ''
  END
)
WHERE base_url = '';
`

// Endpoint quota sidecars: optional per-row quota mode/flow and separately
// encrypted credentials. Defaults: Grok disabled; Tavily/Firecrawl share
// inference credentials. Never copies provider-global Grok admin secrets.
const migration0009 = `
ALTER TABLE provider_keys ADD COLUMN quota_mode TEXT NOT NULL DEFAULT 'disabled';
ALTER TABLE provider_keys ADD COLUMN quota_flow TEXT NOT NULL DEFAULT '';
ALTER TABLE provider_keys ADD COLUMN quota_base_url TEXT;
ALTER TABLE provider_keys ADD COLUMN encrypted_quota_key TEXT;
ALTER TABLE provider_keys ADD COLUMN quota_key_prefix TEXT;
ALTER TABLE provider_keys ADD COLUMN quota_key_fingerprint TEXT;

UPDATE provider_keys SET
  quota_mode = CASE provider
    WHEN 'tavily' THEN 'endpoint_credentials'
    WHEN 'firecrawl' THEN 'endpoint_credentials'
    ELSE 'disabled'
  END,
  quota_flow = CASE provider
    WHEN 'grok' THEN 'grok2api_admin'
    WHEN 'tavily' THEN 'tavily_usage'
    WHEN 'firecrawl' THEN 'firecrawl_credit_usage'
    ELSE ''
  END;
`

const migration0001 = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	id TEXT NOT NULL PRIMARY KEY,
	applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT NOT NULL PRIMARY KEY,
	value TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_tokens (
	id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	token_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	rotated_at TEXT
);

CREATE TABLE IF NOT EXISTS admin_sessions (
	id TEXT NOT NULL PRIMARY KEY,
	token_hash TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	last_seen_at TEXT
);

CREATE TABLE IF NOT EXISTS gateway_keys (
	id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	key_prefix TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	key_hash TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	last_used_at TEXT,
	revoked_at TEXT
);

CREATE TABLE IF NOT EXISTS provider_keys (
	id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	provider TEXT NOT NULL,
	name TEXT NOT NULL,
	encrypted_key TEXT NOT NULL,
	key_prefix TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	cooldown_until TEXT,
	cooldown_reason TEXT,
	last_used_at TEXT,
	last_success_at TEXT,
	last_error_at TEXT,
	last_error_status INTEGER,
	last_error_message_redacted TEXT,
	consecutive_failures INTEGER NOT NULL DEFAULT 0,
	total_failures INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS provider_settings (
	provider TEXT NOT NULL PRIMARY KEY,
	base_url TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
	id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
	occurred_at TEXT NOT NULL,
	actor_kind TEXT NOT NULL,
	actor_id TEXT,
	action TEXT NOT NULL,
	target_kind TEXT,
	target_id TEXT,
	detail_redacted TEXT,
	client_ip_redacted TEXT
);

CREATE TABLE IF NOT EXISTS usage_daily (
	day TEXT NOT NULL,
	gateway_key_id INTEGER,
	provider TEXT NOT NULL,
	route_family TEXT NOT NULL,
	status_class TEXT NOT NULL,
	request_count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (day, gateway_key_id, provider, route_family, status_class)
);
`

func migrate(db *sql.DB) error {
	for _, m := range migrations {
		applied, err := migrationApplied(db, m.id)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return err
		}
	}
	return nil
}

func migrationApplied(db *sql.DB, id string) (bool, error) {
	var exists int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check schema_migrations table: %w", err)
	}
	if exists == 0 {
		return false, nil
	}
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE id = ?`, id).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check migration %s: %w", id, err)
	}
	return count > 0, nil
}

func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("exec migration %s: %w", m.id, err)
	}

	appliedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (id, applied_at) VALUES (?, ?)`,
		m.id, appliedAt,
	); err != nil {
		return fmt.Errorf("record migration %s: %w", m.id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.id, err)
	}
	return nil
}
