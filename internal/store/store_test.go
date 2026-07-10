package store_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"code-guda-gateway/internal/store"
)

var expectedTables = []string{
	"schema_migrations",
	"settings",
	"admin_tokens",
	"admin_sessions",
	"gateway_keys",
	"provider_keys",
	"provider_key_quota_cache",
	"provider_quota_cache",
	"provider_settings",
	"proxy_attempt_logs",
	"audit_events",
	"usage_daily",
}

func TestOpen_MigratesEmptyDB(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	names, err := tableNames(s.DB())
	if err != nil {
		t.Fatalf("tableNames: %v", err)
	}
	for _, want := range expectedTables {
		if !contains(names, want) {
			t.Fatalf("missing table %q; got %v", want, names)
		}
	}
}

func TestOpen_Idempotent(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")

	s1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	s2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer s2.Close()

	var count int
	if err := s2.DB().QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE id = ?`, "0001").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_migrations rows for 0001 = %d, want 1", count)
	}
}

func TestOpen_Migration0008BackfillsProviderEndpointURLs(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE schema_migrations (id TEXT NOT NULL PRIMARY KEY, applied_at TEXT NOT NULL);
		CREATE TABLE provider_settings (provider TEXT NOT NULL PRIMARY KEY, base_url TEXT NOT NULL, updated_at TEXT NOT NULL);
		CREATE TABLE provider_keys (
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
			updated_at TEXT NOT NULL,
			archived_at TEXT,
			last_event_at TEXT,
			last_event_source TEXT,
			last_event_status_class TEXT,
			last_event_http_status INTEGER,
			last_event_message_redacted TEXT,
			last_failed_at TEXT
		);
		INSERT INTO provider_settings(provider, base_url, updated_at)
		VALUES ('grok', 'https://grok.example/v1', 'now');
		INSERT INTO provider_keys(id, provider, name, encrypted_key, key_prefix, fingerprint, created_at, updated_at)
		VALUES
			(41, 'grok', 'configured', 'cipher-a', 'prefix', 'fp-a', 'created', 'updated'),
			(42, 'tavily', 'defaulted', 'cipher-b', 'prefix', 'fp-b', 'created', 'updated');
	`)
	if err != nil {
		t.Fatalf("seed pre-0008 database: %v", err)
	}
	for i := 1; i <= 7; i++ {
		id := fmt.Sprintf("%04d", i)
		if _, err := db.Exec(`INSERT INTO schema_migrations(id, applied_at) VALUES (?, 'now')`, id); err != nil {
			t.Fatalf("mark migration %s: %v", id, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	var configuredURL, defaultURL string
	if err := s.DB().QueryRow(`SELECT base_url FROM provider_keys WHERE id = 41`).Scan(&configuredURL); err != nil {
		t.Fatalf("configured base_url: %v", err)
	}
	if err := s.DB().QueryRow(`SELECT base_url FROM provider_keys WHERE id = 42`).Scan(&defaultURL); err != nil {
		t.Fatalf("default base_url: %v", err)
	}
	if configuredURL != "https://grok.example/v1" {
		t.Fatalf("configured base_url = %q", configuredURL)
	}
	if defaultURL != "https://api.tavily.com" {
		t.Fatalf("default base_url = %q", defaultURL)
	}
	var id int64
	if err := s.DB().QueryRow(`SELECT id FROM provider_keys WHERE name = 'configured'`).Scan(&id); err != nil || id != 41 {
		t.Fatalf("stable id = %d err=%v", id, err)
	}
}

func TestOpen_Migration0009BackfillsEndpointQuotaDefaults(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	// Seed a post-0008 schema with non-default inference routing fields so
	// migration 0009 cannot accidentally rewrite selection/cooldown state.
	_, err = db.Exec(`
		CREATE TABLE schema_migrations (id TEXT NOT NULL PRIMARY KEY, applied_at TEXT NOT NULL);
		CREATE TABLE provider_keys (
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
			updated_at TEXT NOT NULL,
			archived_at TEXT,
			last_event_at TEXT,
			last_event_source TEXT,
			last_event_status_class TEXT,
			last_event_http_status INTEGER,
			last_event_message_redacted TEXT,
			last_failed_at TEXT,
			base_url TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO provider_keys(
			id, provider, name, encrypted_key, key_prefix, fingerprint,
			enabled, cooldown_until, cooldown_reason, last_failed_at,
			created_at, updated_at, base_url
		) VALUES
			(51, 'grok', 'g1', 'cipher-g', 'gpref', 'gfp',
			 1, '2026-07-11T01:00:00Z', 'rate_limit', '2026-07-11T00:30:00Z',
			 'created-g', 'updated-g', 'https://new-api.example/v1'),
			(52, 'tavily', 't1', 'cipher-t', 'tpref', 'tfp',
			 1, '2026-07-11T02:00:00Z', 'quota', '2026-07-11T01:30:00Z',
			 'created-t', 'updated-t', 'https://api.tavily.com'),
			(53, 'firecrawl', 'f1', 'cipher-f', 'fpref', 'ffp',
			 0, NULL, NULL, '2026-07-11T03:00:00Z',
			 'created-f', 'updated-f', 'https://api.firecrawl.dev/v2');
	`)
	if err != nil {
		t.Fatalf("seed pre-0009 database: %v", err)
	}
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("%04d", i)
		if _, err := db.Exec(`INSERT INTO schema_migrations(id, applied_at) VALUES (?, 'now')`, id); err != nil {
			t.Fatalf("mark migration %s: %v", id, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	type row struct {
		id               int64
		provider         string
		baseURL          string
		encryptedKey     string
		cooldownUntil    sql.NullString
		cooldownReason   sql.NullString
		lastFailedAt     sql.NullString
		quotaMode        string
		quotaFlow        string
		quotaBaseURL     sql.NullString
		encryptedQuota   sql.NullString
		quotaKeyPrefix   sql.NullString
		quotaFingerprint sql.NullString
	}
	load := func(id int64) row {
		t.Helper()
		var r row
		err := s.DB().QueryRow(`
			SELECT id, provider, base_url, encrypted_key, cooldown_until, cooldown_reason, last_failed_at,
			       quota_mode, quota_flow, quota_base_url, encrypted_quota_key, quota_key_prefix, quota_key_fingerprint
			FROM provider_keys WHERE id = ?`, id).Scan(
			&r.id, &r.provider, &r.baseURL, &r.encryptedKey, &r.cooldownUntil, &r.cooldownReason, &r.lastFailedAt,
			&r.quotaMode, &r.quotaFlow, &r.quotaBaseURL, &r.encryptedQuota, &r.quotaKeyPrefix, &r.quotaFingerprint,
		)
		if err != nil {
			t.Fatalf("load id=%d: %v", id, err)
		}
		return r
	}

	want := []struct {
		id             int64
		provider       string
		baseURL        string
		encryptedKey   string
		cooldownUntil  string
		cooldownReason string
		lastFailedAt   string
		quotaMode      string
		quotaFlow      string
	}{
		{51, "grok", "https://new-api.example/v1", "cipher-g", "2026-07-11T01:00:00Z", "rate_limit", "2026-07-11T00:30:00Z", "disabled", "grok2api_admin"},
		{52, "tavily", "https://api.tavily.com", "cipher-t", "2026-07-11T02:00:00Z", "quota", "2026-07-11T01:30:00Z", "endpoint_credentials", "tavily_usage"},
		{53, "firecrawl", "https://api.firecrawl.dev/v2", "cipher-f", "", "", "2026-07-11T03:00:00Z", "endpoint_credentials", "firecrawl_credit_usage"},
	}
	for _, w := range want {
		got := load(w.id)
		if got.provider != w.provider {
			t.Fatalf("id=%d provider = %q, want %q", w.id, got.provider, w.provider)
		}
		if got.baseURL != w.baseURL {
			t.Fatalf("id=%d base_url changed: got %q want %q", w.id, got.baseURL, w.baseURL)
		}
		if got.encryptedKey != w.encryptedKey {
			t.Fatalf("id=%d encrypted_key changed: got %q want %q", w.id, got.encryptedKey, w.encryptedKey)
		}
		if nullString(got.cooldownUntil) != w.cooldownUntil {
			t.Fatalf("id=%d cooldown_until changed: got %q want %q", w.id, nullString(got.cooldownUntil), w.cooldownUntil)
		}
		if nullString(got.cooldownReason) != w.cooldownReason {
			t.Fatalf("id=%d cooldown_reason changed: got %q want %q", w.id, nullString(got.cooldownReason), w.cooldownReason)
		}
		if nullString(got.lastFailedAt) != w.lastFailedAt {
			t.Fatalf("id=%d last_failed_at changed: got %q want %q", w.id, nullString(got.lastFailedAt), w.lastFailedAt)
		}
		if got.quotaMode != w.quotaMode {
			t.Fatalf("id=%d quota_mode = %q, want %q", w.id, got.quotaMode, w.quotaMode)
		}
		if got.quotaFlow != w.quotaFlow {
			t.Fatalf("id=%d quota_flow = %q, want %q", w.id, got.quotaFlow, w.quotaFlow)
		}
		if got.quotaBaseURL.Valid {
			t.Fatalf("id=%d quota_base_url should be NULL after backfill, got %q", w.id, got.quotaBaseURL.String)
		}
		if got.encryptedQuota.Valid {
			t.Fatalf("id=%d encrypted_quota_key should be NULL after backfill, got %q", w.id, got.encryptedQuota.String)
		}
		if got.quotaKeyPrefix.Valid {
			t.Fatalf("id=%d quota_key_prefix should be NULL after backfill, got %q", w.id, got.quotaKeyPrefix.String)
		}
		if got.quotaFingerprint.Valid {
			t.Fatalf("id=%d quota_key_fingerprint should be NULL after backfill, got %q", w.id, got.quotaFingerprint.String)
		}
	}

	// New columns must exist; raw-style secret columns must not.
	cols, err := tableColumnNames(s.DB(), "provider_keys")
	if err != nil {
		t.Fatalf("tableColumnNames: %v", err)
	}
	for _, col := range []string{"quota_mode", "quota_flow", "quota_base_url", "encrypted_quota_key", "quota_key_prefix", "quota_key_fingerprint"} {
		if !contains(cols, col) {
			t.Fatalf("provider_keys missing column %q; got %v", col, cols)
		}
	}
}

func TestOpen_SetsPragmas(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	var journalMode string
	if err := s.DB().QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	var busyTimeout int
	if err := s.DB().QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}
}

func TestOpen_InvalidPath(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "missing", "subdir", "gateway.db")

	_, err := store.Open(dbPath)
	if err == nil {
		t.Fatal("Open: expected error for non-existent parent directory")
	}
}

func TestMigrate_DoesNotStoreSecrets(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")

	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	forbiddenColumns := []string{"secret", "plaintext", "raw_token", "raw_key", "api_key"}

	type secretTableSpec struct {
		table        string
		requiredHash string
	}
	specs := []secretTableSpec{
		{table: "gateway_keys", requiredHash: "key_hash"},
		{table: "admin_tokens", requiredHash: "token_hash"},
		{table: "provider_keys", requiredHash: "encrypted_key"},
	}

	for _, spec := range specs {
		cols, err := tableColumnNames(s.DB(), spec.table)
		if err != nil {
			t.Fatalf("%s: tableColumnNames: %v", spec.table, err)
		}
		if !contains(cols, spec.requiredHash) {
			t.Fatalf("%s: missing required column %q; got %v", spec.table, spec.requiredHash, cols)
		}
		for _, col := range cols {
			for _, forbidden := range forbiddenColumns {
				if strings.EqualFold(col, forbidden) {
					t.Fatalf("%s: forbidden plaintext-style column %q", spec.table, col)
				}
			}
		}
	}
}

func TestMigrate_AdminUIV2Columns(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	required := []struct {
		table  string
		column string
	}{
		{table: "admin_sessions", column: "csrf_token_hash"},
		{table: "provider_keys", column: "archived_at"},
		{table: "provider_keys", column: "last_event_at"},
		{table: "provider_keys", column: "last_event_source"},
		{table: "provider_keys", column: "last_event_status_class"},
		{table: "provider_keys", column: "last_event_http_status"},
		{table: "provider_keys", column: "last_event_message_redacted"},
		{table: "provider_quota_cache", column: "provider"},
	}
	for _, req := range required {
		cols, err := tableColumnNames(s.DB(), req.table)
		if err != nil {
			t.Fatalf("%s: tableColumnNames: %v", req.table, err)
		}
		if !contains(cols, req.column) {
			t.Fatalf("%s missing column %q; got %v", req.table, req.column, cols)
		}
	}
}

func TestMigrate_MultiKeyObservabilityTables(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	required := []struct {
		table  string
		column string
	}{
		{table: "provider_key_quota_cache", column: "provider_key_id"},
		{table: "provider_key_quota_cache", column: "details_json"},
		{table: "proxy_attempt_logs", column: "request_id"},
		{table: "proxy_attempt_logs", column: "attempt_index"},
		{table: "proxy_attempt_logs", column: "terminal"},
	}
	for _, req := range required {
		cols, err := tableColumnNames(s.DB(), req.table)
		if err != nil {
			t.Fatalf("%s: tableColumnNames: %v", req.table, err)
		}
		if !contains(cols, req.column) {
			t.Fatalf("%s missing column %q; got %v", req.table, req.column, cols)
		}
	}
}

func tableColumnNames(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var cid int
		var name, colType string
		var notnull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func tableNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func nullString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}
