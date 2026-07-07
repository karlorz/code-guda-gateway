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
		id: "0001",
		sql: migration0001,
	},
	{
		id: "0002",
		sql: migration0002,
	},
}

const migration0002 = `
ALTER TABLE admin_tokens ADD COLUMN key_prefix TEXT NOT NULL DEFAULT '';
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