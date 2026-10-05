package store_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"code-guda-gateway/internal/store"
)

func TestMigration0011_IssuanceIdentity(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_m0011.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// 1. Verify invite_codes table exists with expected columns
	cols, err := tableColumnNames(st.DB(), "invite_codes")
	if err != nil {
		t.Fatalf("tableColumnNames invite_codes: %v", err)
	}
	expectedInviteCols := []string{
		"id", "code", "agent_label_bind", "max_redemptions",
		"redemption_count", "expires_at", "revoked_at", "created_at",
	}
	for _, col := range expectedInviteCols {
		if !contains(cols, col) {
			t.Errorf("invite_codes missing column %s", col)
		}
	}

	// 2. Verify gateway_keys has agent_label, issued_via, ref_code_id
	gwCols, err := tableColumnNames(st.DB(), "gateway_keys")
	if err != nil {
		t.Fatalf("tableColumnNames gateway_keys: %v", err)
	}
	for _, col := range []string{"agent_label", "issued_via", "ref_code_id"} {
		if !contains(gwCols, col) {
			t.Errorf("gateway_keys missing column %s", col)
		}
	}

	// 3. Verify oauth_codes has agent_label, issued_via, ref_code_id
	ocCols, err := tableColumnNames(st.DB(), "oauth_codes")
	if err != nil {
		t.Fatalf("tableColumnNames oauth_codes: %v", err)
	}
	for _, col := range []string{"agent_label", "issued_via", "ref_code_id"} {
		if !contains(ocCols, col) {
			t.Errorf("oauth_codes missing column %s", col)
		}
	}

	// 4. Verify settings has public_issuance = off
	var val string
	err = st.DB().QueryRow(`SELECT value FROM settings WHERE key = 'public_issuance'`).Scan(&val)
	if err != nil {
		t.Fatalf("select public_issuance: %v", err)
	}
	if val != "off" {
		t.Errorf("public_issuance = %s, want off", val)
	}
}

func TestMigration0011_BackfillExistingGatewayKeys(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_m0011_backfill.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	// Seed up to migration 0010
	_, err = db.Exec(`
		CREATE TABLE schema_migrations (id TEXT NOT NULL PRIMARY KEY, applied_at TEXT NOT NULL);
		CREATE TABLE settings (key TEXT NOT NULL PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL);
		CREATE TABLE gateway_keys (
			id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			key_prefix TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			last_used_at TEXT,
			revoked_at TEXT,
			oauth_owned INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE oauth_clients (client_id TEXT PRIMARY KEY, client_name TEXT, redirect_uris TEXT NOT NULL, created_at TEXT NOT NULL);
		CREATE TABLE oauth_codes (code_hash TEXT PRIMARY KEY, client_id TEXT NOT NULL, redirect_uri TEXT NOT NULL, code_challenge TEXT NOT NULL, code_challenge_method TEXT NOT NULL, expires_at TEXT NOT NULL, created_at TEXT NOT NULL);
		CREATE TABLE oauth_grants (refresh_hash TEXT PRIMARY KEY, gateway_key_id INTEGER NOT NULL UNIQUE, client_id TEXT NOT NULL, access_expires_at TEXT NOT NULL, refresh_expires_at TEXT NOT NULL, created_at TEXT NOT NULL);
		INSERT INTO gateway_keys (id, name, key_prefix, fingerprint, key_hash, created_at)
		VALUES
			(1, 'my-manual-key', 'gsk_123', 'fp1', 'hash1', '2026-01-01T00:00:00Z'),
			(2, 'oauth:chatgpt-agent', 'gsk_456', 'fp2', 'hash2', '2026-01-01T00:00:00Z');
	`)
	if err != nil {
		t.Fatalf("seed pre-0011 db: %v", err)
	}
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("%04d", i)
		if _, err := db.Exec(`INSERT INTO schema_migrations (id, applied_at) VALUES (?, 'now')`, id); err != nil {
			t.Fatalf("mark migration %s: %v", id, err)
		}
	}
	_ = db.Close()

	// Open with store to apply 0011
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	var label1, via1 string
	if err := st.DB().QueryRow(`SELECT agent_label, issued_via FROM gateway_keys WHERE id = 1`).Scan(&label1, &via1); err != nil {
		t.Fatalf("scan key 1: %v", err)
	}
	if label1 != "my-manual-key" || via1 != "operator" {
		t.Errorf("key 1: got label=%q via=%q, want 'my-manual-key', 'operator'", label1, via1)
	}

	var label2, via2 string
	if err := st.DB().QueryRow(`SELECT agent_label, issued_via FROM gateway_keys WHERE id = 2`).Scan(&label2, &via2); err != nil {
		t.Fatalf("scan key 2: %v", err)
	}
	if label2 != "chatgpt-agent" || via2 != "operator" {
		t.Errorf("key 2: got label=%q via=%q, want 'chatgpt-agent', 'operator'", label2, via2)
	}
}
