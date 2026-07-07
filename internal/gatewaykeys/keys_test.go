package gatewaykeys_test

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/store"
)

func openTestService(t *testing.T) (*gatewaykeys.Service, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return gatewaykeys.NewService(st.DB()), st
}

func TestCreate_StoresHashReturnsRaw(t *testing.T) {
	t.Parallel()
	svc, st := openTestService(t)

	raw, _, err := svc.Create("prod")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !regexp.MustCompile(`^gsk_[A-Za-z0-9]{32}$`).MatchString(raw) {
		t.Fatalf("raw key format: %q", raw)
	}

	var hash, prefix, fingerprint string
	err = st.DB().QueryRow(
		`SELECT key_hash, key_prefix, fingerprint FROM gateway_keys WHERE name = ?`, "prod",
	).Scan(&hash, &prefix, &fingerprint)
	if err != nil {
		t.Fatalf("query gateway_keys: %v", err)
	}
	if hash == raw {
		t.Fatal("stored key_hash equals raw key")
	}
	if len(hash) != 64 {
		t.Fatalf("key_hash hex len = %d, want 64", len(hash))
	}
	if prefix != raw[:8] {
		t.Fatalf("key_prefix = %q, want %q", prefix, raw[:8])
	}
	if fingerprint == "" || fingerprint == raw {
		t.Fatalf("fingerprint = %q", fingerprint)
	}

	var blob string
	rows, err := st.DB().Query(`SELECT key_hash, key_prefix, fingerprint, name FROM gateway_keys`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h, p, f, n string
		_ = rows.Scan(&h, &p, &f, &n)
		blob += h + p + f + n
	}
	if strings.Contains(blob, raw) {
		t.Fatal("raw key appears in gateway_keys columns")
	}
}

func TestList_ReturnsOnlyDisplayFields(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw1, _, _ := svc.Create("a")
	raw2, _, _ := svc.Create("b")

	list, err := svc.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}
	for _, item := range list {
		if item.Name == "" || item.Prefix == "" || item.Fingerprint == "" {
			t.Fatalf("missing display field: %+v", item)
		}
		if !item.Enabled {
			t.Fatalf("new key should be enabled: %+v", item)
		}
	}
	var b strings.Builder
	for _, k := range list {
		b.WriteString(k.Name)
		b.WriteString(k.Prefix)
		b.WriteString(k.Fingerprint)
	}
	blob := b.String()
	if strings.Contains(blob, raw1) || strings.Contains(blob, raw2) {
		t.Fatal("List leaked raw keys")
	}
}

func TestVerify_AcceptsRawRejectsBogus(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, _, err := svc.Create("k1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec, err := svc.Verify(raw)
	if err != nil {
		t.Fatalf("Verify(raw): %v", err)
	}
	if rec == nil || !rec.Enabled {
		t.Fatalf("Verify(raw): rec=%+v", rec)
	}
	rec, err = svc.Verify("gsk_bogus")
	if err != nil || rec != nil {
		t.Fatalf("Verify(bogus): rec=%v err=%v", rec, err)
	}
	rec, err = svc.Verify("")
	if err != nil || rec != nil {
		t.Fatalf("Verify(empty): rec=%v err=%v", rec, err)
	}
}

func TestVerify_RejectsDisabled(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, disp, _ := svc.Create("k1")
	if err := svc.Disable(disp.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	rec, err := svc.Verify(raw)
	if err != gatewaykeys.ErrNotAuthorized {
		t.Fatalf("Verify disabled: rec=%v err=%v want ErrNotAuthorized", rec, err)
	}
}

func TestVerify_RejectsRevoked(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, disp, _ := svc.Create("k1")
	if err := svc.Revoke(disp.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	rec, err := svc.Verify(raw)
	if err != gatewaykeys.ErrNotAuthorized {
		t.Fatalf("Verify revoked: rec=%v err=%v", rec, err)
	}
}

func TestVerify_UpdatesLastUsedAt(t *testing.T) {
	t.Parallel()
	svc, st := openTestService(t)
	raw, disp, _ := svc.Create("k1")

	before := time.Now().UTC().Add(-time.Minute)
	rec, err := svc.Verify(raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rec.LastUsedAt == nil {
		t.Fatal("LastUsedAt nil after Verify")
	}
	parsed, err := time.Parse(time.RFC3339Nano, *rec.LastUsedAt)
	if err != nil {
		t.Fatalf("parse last_used_at: %v", err)
	}
	if parsed.Before(before) {
		t.Fatalf("last_used_at %v before %v", parsed, before)
	}

	var dbLast sql.NullString
	_ = st.DB().QueryRow(`SELECT last_used_at FROM gateway_keys WHERE id = ?`, disp.ID).Scan(&dbLast)
	if !dbLast.Valid {
		t.Fatal("DB last_used_at null")
	}
}

func TestEnable_Reenables(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, disp, _ := svc.Create("k1")
	_ = svc.Disable(disp.ID)
	_ = svc.Enable(disp.ID)
	rec, err := svc.Verify(raw)
	if err != nil || rec == nil {
		t.Fatalf("Verify after enable: rec=%v err=%v", rec, err)
	}
}

func TestDelete_RemovesRow(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, disp, _ := svc.Create("k1")
	if err := svc.Delete(disp.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	rec, err := svc.Verify(raw)
	if err != nil || rec != nil {
		t.Fatalf("Verify after delete: rec=%v err=%v", rec, err)
	}
}