package store_test

import (
	"database/sql"
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
