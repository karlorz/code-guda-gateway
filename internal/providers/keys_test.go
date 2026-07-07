package providers_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/store"
)

func openKeyRepo(t *testing.T) (*providers.KeyRepo, *store.Store, []byte) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mkPath := filepath.Join(t.TempDir(), "master.key")
	mk, err := secrets.LoadOrCreate(mkPath)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return providers.NewKeyRepo(st.DB(), mk), st, mk
}

func TestAddKey_EncryptsAtRest(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	raw := "xai-sk-OOHa-secret-key-material-here"
	_, err := repo.Add(providers.ProviderGrok, "primary", raw)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	var enc string
	err = st.DB().QueryRow(
		`SELECT encrypted_key FROM provider_keys WHERE name = ?`, "primary",
	).Scan(&enc)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if enc == raw {
		t.Fatal("encrypted_key equals raw key")
	}
	if strings.Contains(enc, raw) {
		t.Fatal("raw key appears in encrypted_key column")
	}
}

func TestListKeys_MasksRawKey(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	raw1 := "tvly-dev-aaaa1111bbbb2222"
	raw2 := "tvly-dev-cccc3333dddd4444"
	_, _ = repo.Add(providers.ProviderTavily, "a", raw1)
	_, _ = repo.Add(providers.ProviderTavily, "b", raw2)

	list, err := repo.List(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	var blob strings.Builder
	for _, k := range list {
		if k.KeyPrefix == "" || k.Fingerprint == "" {
			t.Fatalf("missing prefix/fingerprint: %+v", k)
		}
		if len(k.KeyPrefix) != 6 {
			t.Fatalf("key_prefix len = %d, want 6: %q", len(k.KeyPrefix), k.KeyPrefix)
		}
		blob.WriteString(k.Name)
		blob.WriteString(k.KeyPrefix)
		blob.WriteString(k.Fingerprint)
	}
	s := blob.String()
	if strings.Contains(s, raw1) || strings.Contains(s, raw2) {
		t.Fatal("List leaked raw keys")
	}
}

func TestSelectKey_ReturnsEnabledNonCooled(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	raw1 := "xai-key-one-1111111111111111"
	raw2 := "xai-key-two-2222222222222222"
	_, _ = repo.Add(providers.ProviderGrok, "k1", raw1)
	_, _ = repo.Add(providers.ProviderGrok, "k2", raw2)

	id, got, err := repo.SelectKey(providers.ProviderGrok)
	if err != nil {
		t.Fatalf("SelectKey: %v", err)
	}
	if id <= 0 {
		t.Fatalf("id = %d", id)
	}
	if got != raw1 && got != raw2 {
		t.Fatalf("decrypted key mismatch: %q", got)
	}

	_, got2, err := repo.SelectKey(providers.ProviderGrok)
	if err != nil {
		t.Fatalf("SelectKey again: %v", err)
	}
	if got2 != raw1 && got2 != raw2 {
		t.Fatalf("second SelectKey: %q", got2)
	}
}

func TestSelectKey_SkipsDisabled(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	_, d1 := mustAdd(t, repo, providers.ProviderGrok, "only", "xai-only-enabled-key-12345")
	_, d2 := mustAdd(t, repo, providers.ProviderGrok, "disabled", "xai-disabled-key-678901234")
	if err := repo.Disable(d2.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	for i := 0; i < 5; i++ {
		id, _, err := repo.SelectKey(providers.ProviderGrok)
		if err != nil {
			t.Fatalf("SelectKey: %v", err)
		}
		if id == d2.ID {
			t.Fatalf("SelectKey returned disabled key id %d", d2.ID)
		}
		if id != d1.ID {
			t.Fatalf("want id %d, got %d", d1.ID, id)
		}
	}
}

func TestSelectKey_SkipsCooledDown(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	_, d1 := mustAdd(t, repo, providers.ProviderGrok, "ok", "xai-ok-key-1111111111111111")
	_, d2 := mustAdd(t, repo, providers.ProviderGrok, "cold", "xai-cold-key-2222222222222222")

	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	_, err := st.DB().Exec(
		`UPDATE provider_keys SET cooldown_until = ?, cooldown_reason = ? WHERE id = ?`,
		future, "rate_limited", d2.ID,
	)
	if err != nil {
		t.Fatalf("set cooldown: %v", err)
	}

	for i := 0; i < 5; i++ {
		id, _, err := repo.SelectKey(providers.ProviderGrok)
		if err != nil {
			t.Fatalf("SelectKey: %v", err)
		}
		if id == d2.ID {
			t.Fatal("SelectKey returned cooled-down key")
		}
		if id != d1.ID {
			t.Fatalf("want %d, got %d", d1.ID, id)
		}
	}
}

func TestSelectKey_UpdatesLastUsedAt(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	_, d := mustAdd(t, repo, providers.ProviderGrok, "u", "xai-used-key-3333333333333333")

	before := time.Now().UTC().Add(-time.Minute)
	id, _, err := repo.SelectKey(providers.ProviderGrok)
	if err != nil {
		t.Fatalf("SelectKey: %v", err)
	}
	if id != d.ID {
		t.Fatalf("id = %d, want %d", id, d.ID)
	}

	var lastUsed string
	err = st.DB().QueryRow(`SELECT last_used_at FROM provider_keys WHERE id = ?`, id).Scan(&lastUsed)
	if err != nil {
		t.Fatalf("query last_used_at: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, lastUsed)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Before(before) {
		t.Fatalf("last_used_at %v before %v", parsed, before)
	}
}

func TestSelectKey_NoEnabledKeysReturnsError(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	_, d := mustAdd(t, repo, providers.ProviderGrok, "x", "xai-all-off-4444444444444444")
	_ = repo.Disable(d.ID)

	_, _, err := repo.SelectKey(providers.ProviderGrok)
	if !errors.Is(err, providers.ErrNoEnabledKey) {
		t.Fatalf("SelectKey err = %v, want ErrNoEnabledKey", err)
	}
}

func TestProviderKeyArchiveRestoreDisabled(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	key, err := repo.Add(providers.ProviderGrok, "primary", "sk-test-provider-key-123456789")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := repo.Archive(key.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, _, err := repo.SelectKey(providers.ProviderGrok); !errors.Is(err, providers.ErrNoEnabledKey) {
		t.Fatalf("SelectKey after archive err = %v, want ErrNoEnabledKey", err)
	}
	if err := repo.RestoreArchived(key.ID); err != nil {
		t.Fatalf("RestoreArchived: %v", err)
	}
	got, err := repo.Get(key.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Enabled {
		t.Fatal("restored provider key enabled = true, want false")
	}
	if got.ArchivedAt != nil {
		t.Fatalf("archived_at after restore = %v, want nil", *got.ArchivedAt)
	}
}

func TestProviderKeyLastEventRedactsMessage(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	key, err := repo.Add(providers.ProviderGrok, "event", "xai-event-key-123456789")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	status := 429
	if err := repo.MarkLastEvent(key.ID, providers.LastEvent{
		Source:      "manual_test",
		StatusClass: "4xx",
		HTTPStatus:  &status,
		Message:     "Authorization: Bearer sk-secret-value",
	}); err != nil {
		t.Fatalf("MarkLastEvent: %v", err)
	}
	got, err := repo.Get(key.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastEventAt == nil || got.LastEventSource == nil || *got.LastEventSource != "manual_test" {
		t.Fatalf("last event fields not set: %+v", got)
	}
	if got.LastEventHTTPStatus == nil || *got.LastEventHTTPStatus != status {
		t.Fatalf("last status = %v, want %d", got.LastEventHTTPStatus, status)
	}
	if got.LastEventMessageRedacted == nil || strings.Contains(*got.LastEventMessageRedacted, "sk-secret") {
		t.Fatalf("last event message leaked: %v", got.LastEventMessageRedacted)
	}
}

func TestAdd_RejectsDuplicateName(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	if _, err := repo.Add(providers.ProviderGrok, "primary", "xai-dup-key-1111111111111111"); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	_, err := repo.Add(providers.ProviderGrok, "primary", "xai-dup-key-2222222222222222")
	if !errors.Is(err, providers.ErrDuplicateName) {
		t.Fatalf("second Add err = %v, want ErrDuplicateName", err)
	}
	// Same name on a different provider is allowed.
	if _, err := repo.Add(providers.ProviderTavily, "primary", "tvly-dup-key-3333333333333333"); err != nil {
		t.Fatalf("Add other provider same name: %v", err)
	}
}

func TestAdd_RejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	_, err := repo.Add("bogus", "k", "some-raw-key-material-12345")
	if !errors.Is(err, providers.ErrUnknownProvider) {
		t.Fatalf("Add err = %v, want ErrUnknownProvider", err)
	}
}

func TestMarkSuccess_UpdatesFields(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	_, d := mustAdd(t, repo, providers.ProviderGrok, "s", "xai-success-key-555555555555")
	_ = repo.MarkFailure(d.ID, 500, "upstream_error")
	_ = repo.MarkFailure(d.ID, 500, "upstream_error")

	if err := repo.MarkSuccess(d.ID); err != nil {
		t.Fatalf("MarkSuccess: %v", err)
	}

	var lastSuccess string
	var consecutive int
	err := st.DB().QueryRow(
		`SELECT last_success_at, consecutive_failures FROM provider_keys WHERE id = ?`, d.ID,
	).Scan(&lastSuccess, &consecutive)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if lastSuccess == "" {
		t.Fatal("last_success_at empty")
	}
	if consecutive != 0 {
		t.Fatalf("consecutive_failures = %d, want 0", consecutive)
	}
}

func TestMarkFailure_UpdatesFields(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	_, d := mustAdd(t, repo, providers.ProviderGrok, "f", "xai-fail-key-6666666666666666")

	body := `error: Bearer sk-supersecret and api_key=tvly-leaked in response`
	redacted := providers.Redact(body)
	if strings.Contains(redacted, "sk-supersecret") || strings.Contains(redacted, "tvly-leaked") {
		t.Fatalf("Redact leaked: %q", redacted)
	}

	if err := repo.MarkFailure(d.ID, 429, redacted); err != nil {
		t.Fatalf("MarkFailure: %v", err)
	}

	var (
		lastErrAt, msg string
		status         int
		consec, total  int
	)
	err := st.DB().QueryRow(`
		SELECT last_error_at, last_error_status, last_error_message_redacted,
		       consecutive_failures, total_failures
		FROM provider_keys WHERE id = ?`, d.ID,
	).Scan(&lastErrAt, &status, &msg, &consec, &total)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if lastErrAt == "" || status != 429 {
		t.Fatalf("last_error fields: at=%q status=%d", lastErrAt, status)
	}
	if strings.Contains(msg, "sk-supersecret") {
		t.Fatalf("stored message leaked key: %q", msg)
	}
	if consec < 1 || total < 1 {
		t.Fatalf("failures consec=%d total=%d", consec, total)
	}
}

func TestRedact_StripsKeyMaterial(t *testing.T) {
	t.Parallel()
	cases := []string{
		"Authorization: Bearer sk-abc123xyz789012345678901234567890",
		"failed with api_key=tvly-dev-secretvaluehere",
		strings.Repeat("upstream error detail ", 50),
	}
	for _, in := range cases {
		out := providers.Redact(in)
		if strings.Contains(out, "sk-abc123") {
			t.Fatalf("Bearer key leaked in %q -> %q", in, out)
		}
		if strings.Contains(out, "tvly-dev-secret") {
			t.Fatalf("api_key leaked in %q -> %q", in, out)
		}
		if len(out) > 512 {
			t.Fatalf("redact output too long: %d", len(out))
		}
	}
}

func mustAdd(t *testing.T, repo *providers.KeyRepo, provider, name, raw string) (string, providers.DisplayProviderKey) {
	t.Helper()
	d, err := repo.Add(provider, name, raw)
	if err != nil {
		t.Fatalf("Add(%s): %v", name, err)
	}
	return raw, d
}
