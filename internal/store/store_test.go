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
	"provider_settings",
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

	rows, err := s.DB().Query(`SELECT sql FROM sqlite_master WHERE type = 'table' AND sql IS NOT NULL`)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()

	var combined strings.Builder
	for rows.Next() {
		var sqlText string
		if err := rows.Scan(&sqlText); err != nil {
			t.Fatalf("scan: %v", err)
		}
		combined.WriteString(strings.ToLower(sqlText))
		combined.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	schema := combined.String()
	for _, forbidden := range []string{"api_key_plaintext", "raw_secret"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("schema must not contain %q", forbidden)
		}
	}
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