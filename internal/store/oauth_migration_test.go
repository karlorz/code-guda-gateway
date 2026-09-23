package store_test

import (
	"path/filepath"
	"testing"

	"code-guda-gateway/internal/store"
)

func TestMigration0010_OAuthTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_m0010.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// Verify tables exist
	tables := []string{"oauth_clients", "oauth_codes", "oauth_grants"}
	for _, table := range tables {
		var count int
		err := st.DB().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count)
		if err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s not created by migration 0010", table)
		}
	}
}
