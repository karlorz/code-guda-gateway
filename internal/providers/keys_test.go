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

func TestRawKey_RoundTripAndMissing(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	raw := "tvly-rawkey-roundtrip-secret-xyz"
	d, err := repo.Add(providers.ProviderTavily, "raw1", raw)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := repo.RawKey(d.ID)
	if err != nil {
		t.Fatalf("RawKey: %v", err)
	}
	if got != raw {
		t.Fatalf("RawKey = %q, want %q", got, raw)
	}
	// SelectKey updates last_used_at; RawKey must not.
	after, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.LastUsedAt != nil {
		t.Fatalf("RawKey should not set last_used_at, got %v", *after.LastUsedAt)
	}
	if _, err := repo.RawKey(999999); err == nil {
		t.Fatal("expected error for nonexistent id")
	}
}

func TestSelectKey_DemotesFailedKeyToEnd(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	_, d1 := mustAdd(t, repo, providers.ProviderTavily, "a", "tvly-key-aaaa1111bbbb2222")
	_, d2 := mustAdd(t, repo, providers.ProviderTavily, "b", "tvly-key-cccc3333dddd4444")

	// Default: lowest id first.
	id, _, err := repo.SelectKey(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("SelectKey: %v", err)
	}
	if id != d1.ID {
		t.Fatalf("first select = %d, want %d", id, d1.ID)
	}

	until := time.Now().UTC().Add(time.Hour)
	reason := "plan_limit_exceeded"
	if err := repo.MarkFailureWithCooldown(d1.ID, 432, "plan limit", &until, &reason); err != nil {
		t.Fatalf("MarkFailureWithCooldown: %v", err)
	}
	// While cooled, only d2.
	id, _, err = repo.SelectKey(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("SelectKey cooled: %v", err)
	}
	if id != d2.ID {
		t.Fatalf("while cooled got %d, want %d", id, d2.ID)
	}

	// Expire cooldown but keep last_failed_at demotion.
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(
		`UPDATE provider_keys SET cooldown_until = ?, cooldown_reason = ? WHERE id = ?`,
		past, "plan_limit_exceeded", d1.ID,
	); err != nil {
		t.Fatalf("expire cool: %v", err)
	}
	id, _, err = repo.SelectKey(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("SelectKey demoted: %v", err)
	}
	if id != d2.ID {
		t.Fatalf("after demote got %d, want never-failed %d", id, d2.ID)
	}

	// Promote d1 (clear demotion) → back to lowest id among front pack.
	if err := repo.ResetSelection(d1.ID); err != nil {
		t.Fatalf("ResetSelection: %v", err)
	}
	id, _, err = repo.SelectKey(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("SelectKey after promote: %v", err)
	}
	if id != d1.ID {
		t.Fatalf("after promote got %d, want %d", id, d1.ID)
	}
}

func TestMarkSuccess_ClearsLastFailedAt(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	_, d := mustAdd(t, repo, providers.ProviderGrok, "s", "xai-success-demote-55555555")
	if err := repo.DemoteToEnd(d.ID); err != nil {
		t.Fatalf("DemoteToEnd: %v", err)
	}
	got, err := repo.Get(d.ID)
	if err != nil || got.LastFailedAt == nil {
		t.Fatalf("expected last_failed_at set, got %+v err=%v", got, err)
	}
	if err := repo.MarkSuccess(d.ID); err != nil {
		t.Fatalf("MarkSuccess: %v", err)
	}
	got, err = repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastFailedAt != nil {
		t.Fatalf("last_failed_at after success = %v, want nil", *got.LastFailedAt)
	}
}

func TestResetCooldown_ClearsDemotion(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	_, d := mustAdd(t, repo, providers.ProviderGrok, "c", "xai-cool-demote-6666666666")
	until := time.Now().UTC().Add(time.Minute)
	reason := "rate_limited"
	if err := repo.MarkFailureWithCooldown(d.ID, 429, "limited", &until, &reason); err != nil {
		t.Fatalf("MarkFailureWithCooldown: %v", err)
	}
	if err := repo.ResetCooldown(d.ID); err != nil {
		t.Fatalf("ResetCooldown: %v", err)
	}
	got, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CooldownUntil != nil || got.CooldownReason != nil || got.LastFailedAt != nil {
		t.Fatalf("expected clear cool+demote, got until=%v reason=%v failed=%v", got.CooldownUntil, got.CooldownReason, got.LastFailedAt)
	}
}

func TestMarkFailureWithoutCooldown_DoesNotDemote(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	_, d := mustAdd(t, repo, providers.ProviderGrok, "f", "xai-nodemote-777777777777")
	if err := repo.MarkFailure(d.ID, 400, "bad request"); err != nil {
		t.Fatalf("MarkFailure: %v", err)
	}
	got, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastFailedAt != nil {
		t.Fatalf("unexpected demotion on non-cool failure: %v", *got.LastFailedAt)
	}
}

func TestAddEndpoint_StoresNormalizedURLAndEncryptedKey(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	raw := "tvly-endpoint-secret-key-11111"
	d, err := repo.AddEndpoint(providers.ProviderTavily, "ep1", "https://proxy.example/tavily/", raw)
	if err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	if d.BaseURL != "https://proxy.example/tavily" {
		t.Fatalf("display BaseURL = %q, want normalized without trailing slash", d.BaseURL)
	}
	if d.Name != "ep1" || d.Provider != providers.ProviderTavily {
		t.Fatalf("display fields: %+v", d)
	}

	var storedURL, enc string
	if err := st.DB().QueryRow(
		`SELECT base_url, encrypted_key FROM provider_keys WHERE id = ?`, d.ID,
	).Scan(&storedURL, &enc); err != nil {
		t.Fatalf("query: %v", err)
	}
	if storedURL != "https://proxy.example/tavily" {
		t.Fatalf("stored base_url = %q", storedURL)
	}
	if enc == raw || strings.Contains(enc, raw) {
		t.Fatal("raw key appears in encrypted_key")
	}
	got, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BaseURL != "https://proxy.example/tavily" {
		t.Fatalf("Get BaseURL = %q", got.BaseURL)
	}
}

func TestSelectEndpoint_ReturnsURLAndKeyFromSameRow(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	rawA := "tvly-atomic-aaaa1111bbbb2222"
	rawB := "tvly-atomic-cccc3333dddd4444"
	urlA := "https://a.example/v1"
	urlB := "https://b.example/v2"
	a, err := repo.AddEndpoint(providers.ProviderTavily, "a", urlA, rawA)
	if err != nil {
		t.Fatalf("AddEndpoint a: %v", err)
	}
	b, err := repo.AddEndpoint(providers.ProviderTavily, "b", urlB, rawB)
	if err != nil {
		t.Fatalf("AddEndpoint b: %v", err)
	}
	wantByID := map[int64]struct {
		url string
		key string
	}{
		a.ID: {url: urlA, key: rawA},
		b.ID: {url: urlB, key: rawB},
	}
	for i := 0; i < 20; i++ {
		ep, err := repo.SelectEndpoint(providers.ProviderTavily)
		if err != nil {
			t.Fatalf("SelectEndpoint: %v", err)
		}
		want, ok := wantByID[ep.ID]
		if !ok {
			t.Fatalf("unexpected id %d", ep.ID)
		}
		if ep.BaseURL != want.url {
			t.Fatalf("id %d BaseURL = %q, want %q", ep.ID, ep.BaseURL, want.url)
		}
		if ep.APIKey != want.key {
			t.Fatalf("id %d APIKey mismatch for atomic pair", ep.ID)
		}
		if ep.Provider != providers.ProviderTavily {
			t.Fatalf("provider = %q", ep.Provider)
		}
		if ep.BaseURL == urlA && ep.APIKey == rawB {
			t.Fatal("atomic violation: URL A with key B")
		}
		if ep.BaseURL == urlB && ep.APIKey == rawA {
			t.Fatal("atomic violation: URL B with key A")
		}
	}

	if err := repo.Disable(b.ID); err != nil {
		t.Fatalf("Disable b: %v", err)
	}
	ep, err := repo.SelectEndpoint(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("SelectEndpoint a only: %v", err)
	}
	if ep.ID != a.ID || ep.BaseURL != urlA || ep.APIKey != rawA {
		t.Fatalf("want row a pair, got id=%d url=%q key match=%v", ep.ID, ep.BaseURL, ep.APIKey == rawA)
	}
	if err := repo.Enable(b.ID); err != nil {
		t.Fatalf("Enable b: %v", err)
	}
	if err := repo.Disable(a.ID); err != nil {
		t.Fatalf("Disable a: %v", err)
	}
	ep, err = repo.SelectEndpoint(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("SelectEndpoint b only: %v", err)
	}
	if ep.ID != b.ID || ep.BaseURL != urlB || ep.APIKey != rawB {
		t.Fatalf("want row b pair, got id=%d url=%q key match=%v", ep.ID, ep.BaseURL, ep.APIKey == rawB)
	}
}

func TestUpdateBaseURL_ClearsCooldownAndDemotion(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	d, err := repo.AddEndpoint(providers.ProviderGrok, "u", "https://api.x.ai/v1", "xai-update-url-key-11111111")
	if err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	until := time.Now().UTC().Add(time.Hour)
	reason := "rate_limited"
	if err := repo.MarkFailureWithCooldown(d.ID, 429, "limited", &until, &reason); err != nil {
		t.Fatalf("MarkFailureWithCooldown: %v", err)
	}
	before, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}
	if before.CooldownUntil == nil || before.LastFailedAt == nil {
		t.Fatalf("expected cool+demote before update: %+v", before)
	}
	consecBefore := before.ConsecutiveFailures
	totalBefore := before.TotalFailures

	newURL := "https://new.karldigi.dev/v1/"
	if err := repo.UpdateBaseURL(d.ID, newURL); err != nil {
		t.Fatalf("UpdateBaseURL: %v", err)
	}
	got, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if got.BaseURL != "https://new.karldigi.dev/v1" {
		t.Fatalf("BaseURL = %q", got.BaseURL)
	}
	if got.CooldownUntil != nil || got.CooldownReason != nil || got.LastFailedAt != nil {
		t.Fatalf("expected clear cool+demote, got until=%v reason=%v failed=%v",
			got.CooldownUntil, got.CooldownReason, got.LastFailedAt)
	}
	if got.ID != d.ID {
		t.Fatalf("id changed: %d -> %d", d.ID, got.ID)
	}
	if got.ConsecutiveFailures != consecBefore || got.TotalFailures != totalBefore {
		t.Fatalf("counters changed: consec %d->%d total %d->%d",
			consecBefore, got.ConsecutiveFailures, totalBefore, got.TotalFailures)
	}
	if got.Fingerprint != before.Fingerprint {
		t.Fatal("fingerprint should be unchanged on URL update")
	}
}

func TestRotateKey_ClearsCooldownAndDemotionAndChangesFingerprint(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	oldRaw := "xai-rotate-old-key-1111111111"
	d, err := repo.AddEndpoint(providers.ProviderGrok, "r", "https://api.x.ai/v1", oldRaw)
	if err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	until := time.Now().UTC().Add(time.Hour)
	reason := "credential_error"
	if err := repo.MarkFailureWithCooldown(d.ID, 401, "bad key", &until, &reason); err != nil {
		t.Fatalf("MarkFailureWithCooldown: %v", err)
	}
	before, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}
	if before.CooldownUntil == nil || before.LastFailedAt == nil {
		t.Fatalf("expected cool+demote before rotate: %+v", before)
	}
	consecBefore := before.ConsecutiveFailures
	totalBefore := before.TotalFailures
	oldFP := before.Fingerprint
	oldURL := before.BaseURL

	newRaw := "xai-rotate-new-key-2222222222"
	if err := repo.RotateKey(d.ID, newRaw); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	got, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if got.ID != d.ID {
		t.Fatalf("id changed: %d -> %d", d.ID, got.ID)
	}
	if got.BaseURL != oldURL {
		t.Fatalf("BaseURL changed on rotate: %q -> %q", oldURL, got.BaseURL)
	}
	if got.Fingerprint == oldFP {
		t.Fatal("fingerprint should change after rotate")
	}
	if got.CooldownUntil != nil || got.CooldownReason != nil || got.LastFailedAt != nil {
		t.Fatalf("expected clear cool+demote, got until=%v reason=%v failed=%v",
			got.CooldownUntil, got.CooldownReason, got.LastFailedAt)
	}
	if got.ConsecutiveFailures != consecBefore || got.TotalFailures != totalBefore {
		t.Fatalf("counters changed: consec %d->%d total %d->%d",
			consecBefore, got.ConsecutiveFailures, totalBefore, got.TotalFailures)
	}
	raw, err := repo.RawKey(d.ID)
	if err != nil {
		t.Fatalf("RawKey: %v", err)
	}
	if raw != newRaw {
		t.Fatalf("RawKey = %q, want new key", raw)
	}
	ep, err := repo.RawEndpoint(d.ID)
	if err != nil {
		t.Fatalf("RawEndpoint: %v", err)
	}
	if ep.APIKey != newRaw || ep.BaseURL != oldURL || ep.ID != d.ID {
		t.Fatalf("RawEndpoint = %+v", ep)
	}
}

func TestLegacyAdd_SnapshotsProviderDefault(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	settings := providers.NewSettingsRepo(st.DB())
	custom := "https://custom-tavily.example/v1"
	if err := settings.SetBaseURL(providers.ProviderTavily, custom); err != nil {
		t.Fatalf("SetBaseURL: %v", err)
	}
	d, err := repo.Add(providers.ProviderTavily, "legacy", "tvly-legacy-key-material-xyz")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if d.BaseURL != custom {
		t.Fatalf("display BaseURL = %q, want snapshot %q", d.BaseURL, custom)
	}
	var stored string
	if err := st.DB().QueryRow(`SELECT base_url FROM provider_keys WHERE id = ?`, d.ID).Scan(&stored); err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored != custom {
		t.Fatalf("stored base_url = %q, want %q", stored, custom)
	}
	// Changing provider settings later must not rewrite the row.
	if err := settings.SetBaseURL(providers.ProviderTavily, "https://other.example"); err != nil {
		t.Fatalf("SetBaseURL again: %v", err)
	}
	got, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.BaseURL != custom {
		t.Fatalf("BaseURL after settings change = %q, want frozen %q", got.BaseURL, custom)
	}

	// Without custom settings, snapshot compiled default.
	repo2, _, _ := openKeyRepo(t)
	d2, err := repo2.Add(providers.ProviderGrok, "def", "xai-default-snapshot-key-12345")
	if err != nil {
		t.Fatalf("Add grok: %v", err)
	}
	if d2.BaseURL != providers.DefaultGrokBaseURL {
		t.Fatalf("grok snapshot = %q, want %q", d2.BaseURL, providers.DefaultGrokBaseURL)
	}
}

func TestAddEndpointWithQuota_EncryptsInferenceAndQuotaKeysSeparately(t *testing.T) {
	t.Parallel()
	repo, st, mk := openKeyRepo(t)
	infRaw := "xai-inf-secret-key-aaaa1111"
	quotaRaw := "g2a-admin-secret-key-bbbb2222"
	d, err := repo.AddEndpointWithQuota(
		providers.ProviderGrok,
		"sg",
		"https://new-api.example/v1/",
		infRaw,
		providers.EndpointQuotaInput{
			Mode:    providers.QuotaSeparateCredentials,
			Flow:    providers.QuotaFlowGrok2APIAdmin,
			BaseURL: "https://grok2api.example/admin/",
			RawKey:  quotaRaw,
		},
	)
	if err != nil {
		t.Fatalf("AddEndpointWithQuota: %v", err)
	}
	if d.QuotaMode != providers.QuotaSeparateCredentials {
		t.Fatalf("QuotaMode = %q", d.QuotaMode)
	}
	if d.QuotaFlow != providers.QuotaFlowGrok2APIAdmin {
		t.Fatalf("QuotaFlow = %q", d.QuotaFlow)
	}
	if d.QuotaBaseURL == nil || *d.QuotaBaseURL != "https://grok2api.example/admin" {
		t.Fatalf("QuotaBaseURL = %v", d.QuotaBaseURL)
	}
	if !d.QuotaKeyConfigured {
		t.Fatal("QuotaKeyConfigured want true")
	}
	if d.QuotaKeyPrefix == nil || d.QuotaKeyFingerprint == nil {
		t.Fatalf("missing quota key identity: %+v", d)
	}

	var encInf, encQuota []byte
	var qURL string
	if err := st.DB().QueryRow(
		`SELECT encrypted_key, encrypted_quota_key, quota_base_url FROM provider_keys WHERE id = ?`, d.ID,
	).Scan(&encInf, &encQuota, &qURL); err != nil {
		t.Fatalf("query: %v", err)
	}
	if qURL != "https://grok2api.example/admin" {
		t.Fatalf("stored quota_base_url = %q", qURL)
	}
	if string(encInf) == infRaw || strings.Contains(string(encInf), infRaw) {
		t.Fatal("raw inference key stored in encrypted_key")
	}
	if string(encQuota) == quotaRaw || strings.Contains(string(encQuota), quotaRaw) {
		t.Fatal("raw quota key stored in encrypted_quota_key")
	}
	if string(encInf) == string(encQuota) {
		t.Fatal("inference and quota ciphertexts are identical")
	}
	plainInf, err := secrets.Decrypt(mk, encInf)
	if err != nil {
		t.Fatalf("decrypt inference: %v", err)
	}
	plainQuota, err := secrets.Decrypt(mk, encQuota)
	if err != nil {
		t.Fatalf("decrypt quota: %v", err)
	}
	if string(plainInf) != infRaw {
		t.Fatalf("decrypted inference mismatch")
	}
	if string(plainQuota) != quotaRaw {
		t.Fatalf("decrypted quota mismatch")
	}

	blob := d.KeyPrefix + d.Fingerprint + *d.QuotaKeyPrefix + *d.QuotaKeyFingerprint
	if strings.Contains(blob, infRaw) || strings.Contains(blob, quotaRaw) {
		t.Fatal("display leaked raw key material")
	}
}

func TestResolveEndpointQuota_UsesOwningGrokRowOnly(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)

	aInf, aQuota := "xai-a-inf-key-1111111111", "g2a-a-admin-key-aaaaaaaa"
	bInf, bQuota := "xai-b-inf-key-2222222222", "g2a-b-admin-key-bbbbbbbb"
	aURL, aQURL := "https://new-api-a.example/v1", "https://grok2api-a.example"
	bURL, bQURL := "https://new-api-b.example/v1", "https://grok2api-b.example"

	a, err := repo.AddEndpointWithQuota(providers.ProviderGrok, "a", aURL, aInf, providers.EndpointQuotaInput{
		Mode: providers.QuotaSeparateCredentials, Flow: providers.QuotaFlowGrok2APIAdmin,
		BaseURL: aQURL, RawKey: aQuota,
	})
	if err != nil {
		t.Fatalf("add a: %v", err)
	}
	b, err := repo.AddEndpointWithQuota(providers.ProviderGrok, "b", bURL, bInf, providers.EndpointQuotaInput{
		Mode: providers.QuotaSeparateCredentials, Flow: providers.QuotaFlowGrok2APIAdmin,
		BaseURL: bQURL, RawKey: bQuota,
	})
	if err != nil {
		t.Fatalf("add b: %v", err)
	}

	ra, err := repo.ResolveEndpointQuota(a.ID)
	if err != nil {
		t.Fatalf("resolve a: %v", err)
	}
	rb, err := repo.ResolveEndpointQuota(b.ID)
	if err != nil {
		t.Fatalf("resolve b: %v", err)
	}

	if ra.EndpointID != a.ID || ra.Provider != providers.ProviderGrok {
		t.Fatalf("ra identity: %+v", ra)
	}
	if rb.EndpointID != b.ID || rb.Provider != providers.ProviderGrok {
		t.Fatalf("rb identity: %+v", rb)
	}
	if ra.BaseURL != aQURL || ra.APIKey != aQuota {
		t.Fatalf("a resolved cross-row or wrong pair: url=%q key_match=%v", ra.BaseURL, ra.APIKey == aQuota)
	}
	if rb.BaseURL != bQURL || rb.APIKey != bQuota {
		t.Fatalf("b resolved cross-row or wrong pair: url=%q key_match=%v", rb.BaseURL, rb.APIKey == bQuota)
	}
	if ra.BaseURL == bQURL || ra.APIKey == bQuota || ra.APIKey == bInf {
		t.Fatal("row a resolved credentials from row b")
	}
	if rb.BaseURL == aQURL || rb.APIKey == aQuota || rb.APIKey == aInf {
		t.Fatal("row b resolved credentials from row a")
	}
	if ra.APIKey == aInf {
		t.Fatal("separate mode must not return inference key")
	}
	if rb.APIKey == bInf {
		t.Fatal("separate mode must not return inference key")
	}

	ga, err := repo.Get(a.ID)
	if err != nil {
		t.Fatalf("Get a: %v", err)
	}
	if ga.LastUsedAt != nil {
		t.Fatalf("ResolveEndpointQuota set last_used_at on a: %v", *ga.LastUsedAt)
	}
}

func TestResolveEndpointQuota_UsesInferenceCredentialsForSharedMode(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	raw := "tvly-shared-quota-key-1111111"
	url := "https://tavily-proxy.example/v1"
	d, err := repo.AddEndpointWithQuota(providers.ProviderTavily, "shared", url, raw, providers.EndpointQuotaInput{
		Mode: providers.QuotaEndpointCredentials,
		Flow: providers.QuotaFlowTavilyUsage,
	})
	if err != nil {
		t.Fatalf("AddEndpointWithQuota: %v", err)
	}
	if d.QuotaMode != providers.QuotaEndpointCredentials {
		t.Fatalf("QuotaMode = %q", d.QuotaMode)
	}
	if d.QuotaKeyConfigured {
		t.Fatal("shared mode must not report separate key configured")
	}
	if d.QuotaBaseURL != nil {
		t.Fatalf("shared mode QuotaBaseURL = %v, want nil", *d.QuotaBaseURL)
	}

	resolved, err := repo.ResolveEndpointQuota(d.ID)
	if err != nil {
		t.Fatalf("ResolveEndpointQuota: %v", err)
	}
	if resolved.Mode != providers.QuotaEndpointCredentials {
		t.Fatalf("mode = %q", resolved.Mode)
	}
	if resolved.Flow != providers.QuotaFlowTavilyUsage {
		t.Fatalf("flow = %q", resolved.Flow)
	}
	if resolved.BaseURL != url {
		t.Fatalf("BaseURL = %q, want inference %q", resolved.BaseURL, url)
	}
	if resolved.APIKey != raw {
		t.Fatal("shared mode must decrypt inference key")
	}
	after, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.LastUsedAt != nil {
		t.Fatalf("ResolveEndpointQuota must not set last_used_at, got %v", *after.LastUsedAt)
	}
}

func TestResolveEndpointQuota_Disabled(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	d, err := repo.AddEndpointWithQuota(providers.ProviderGrok, "off", "https://api.x.ai/v1", "xai-disabled-quota-key-1111", providers.EndpointQuotaInput{
		Mode: providers.QuotaDisabled,
		Flow: providers.QuotaFlowGrok2APIAdmin,
	})
	if err != nil {
		t.Fatalf("AddEndpointWithQuota: %v", err)
	}
	if d.QuotaMode != providers.QuotaDisabled {
		t.Fatalf("QuotaMode = %q", d.QuotaMode)
	}
	_, err = repo.ResolveEndpointQuota(d.ID)
	if !errors.Is(err, providers.ErrQuotaDisabled) {
		t.Fatalf("err = %v, want ErrQuotaDisabled", err)
	}
}

func TestUpdateEndpointQuota_SwitchAwayDeletesSeparateCiphertext(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	quotaRaw := "g2a-admin-to-delete-key-zzzz"
	d, err := repo.AddEndpointWithQuota(providers.ProviderGrok, "sw", "https://new-api.example/v1", "xai-switch-inf-key-111111", providers.EndpointQuotaInput{
		Mode: providers.QuotaSeparateCredentials, Flow: providers.QuotaFlowGrok2APIAdmin,
		BaseURL: "https://grok2api.example", RawKey: quotaRaw,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var encBefore sqlNullString
	if err := st.DB().QueryRow(`SELECT encrypted_quota_key FROM provider_keys WHERE id = ?`, d.ID).Scan(&encBefore); err != nil {
		t.Fatalf("query before: %v", err)
	}
	if !encBefore.valid || encBefore.s == "" {
		t.Fatal("expected separate ciphertext before switch")
	}

	if err := repo.UpdateEndpointQuota(d.ID, providers.EndpointQuotaInput{
		Mode: providers.QuotaDisabled,
		Flow: providers.QuotaFlowGrok2APIAdmin,
	}); err != nil {
		t.Fatalf("UpdateEndpointQuota: %v", err)
	}
	got, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.QuotaMode != providers.QuotaDisabled {
		t.Fatalf("QuotaMode = %q", got.QuotaMode)
	}
	if got.QuotaBaseURL != nil || got.QuotaKeyConfigured || got.QuotaKeyPrefix != nil || got.QuotaKeyFingerprint != nil {
		t.Fatalf("separate metadata should be cleared: %+v", got)
	}

	var qURL, enc, prefix, fp sqlNullString
	if err := st.DB().QueryRow(
		`SELECT quota_base_url, encrypted_quota_key, quota_key_prefix, quota_key_fingerprint FROM provider_keys WHERE id = ?`, d.ID,
	).Scan(&qURL, &enc, &prefix, &fp); err != nil {
		t.Fatalf("query after: %v", err)
	}
	if qURL.valid || enc.valid || prefix.valid || fp.valid {
		t.Fatalf("expected NULLs after switch, got url=%v enc=%v prefix=%v fp=%v", qURL, enc, prefix, fp)
	}
}

// sqlNullString is a tiny helper for nullable TEXT/BLOB scans in tests.
type sqlNullString struct {
	s     string
	valid bool
}

func (n *sqlNullString) Scan(src any) error {
	if src == nil {
		n.s, n.valid = "", false
		return nil
	}
	switch v := src.(type) {
	case string:
		n.s, n.valid = v, true
	case []byte:
		n.s, n.valid = string(v), true
	default:
		n.s, n.valid = "", false
	}
	return nil
}

func TestRotateEndpointQuotaKey_ChangesFingerprintWithoutChangingInferenceKey(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	infRaw := "xai-rotate-quota-inf-key-1111"
	oldQuota := "g2a-old-admin-key-aaaaaaaaaa"
	d, err := repo.AddEndpointWithQuota(providers.ProviderGrok, "rq", "https://new-api.example/v1", infRaw, providers.EndpointQuotaInput{
		Mode: providers.QuotaSeparateCredentials, Flow: providers.QuotaFlowGrok2APIAdmin,
		BaseURL: "https://grok2api.example", RawKey: oldQuota,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	before, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}
	oldQFP := *before.QuotaKeyFingerprint
	oldInfFP := before.Fingerprint

	newQuota := "g2a-new-admin-key-bbbbbbbbbb"
	if err := repo.RotateEndpointQuotaKey(d.ID, newQuota); err != nil {
		t.Fatalf("RotateEndpointQuotaKey: %v", err)
	}
	after, err := repo.Get(d.ID)
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if after.Fingerprint != oldInfFP {
		t.Fatal("inference fingerprint changed on quota rotate")
	}
	if after.QuotaKeyFingerprint == nil || *after.QuotaKeyFingerprint == oldQFP {
		t.Fatal("quota fingerprint should change")
	}
	rawInf, err := repo.RawKey(d.ID)
	if err != nil {
		t.Fatalf("RawKey: %v", err)
	}
	if rawInf != infRaw {
		t.Fatal("inference key changed on quota rotate")
	}
	resolved, err := repo.ResolveEndpointQuota(d.ID)
	if err != nil {
		t.Fatalf("ResolveEndpointQuota: %v", err)
	}
	if resolved.APIKey != newQuota {
		t.Fatal("resolved quota key is not the rotated key")
	}
	if resolved.APIKey == oldQuota {
		t.Fatal("old quota key still resolved")
	}
}

func TestQuotaMutation_DoesNotChangeInferenceRoutingState(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	d, err := repo.AddEndpointWithQuota(providers.ProviderGrok, "mut", "https://new-api.example/v1", "xai-mut-inf-key-111111111", providers.EndpointQuotaInput{
		Mode: providers.QuotaSeparateCredentials, Flow: providers.QuotaFlowGrok2APIAdmin,
		BaseURL: "https://grok2api.example", RawKey: "g2a-mut-admin-key-11111111",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	until := time.Now().UTC().Add(time.Hour)
	reason := "rate_limited"
	if err := repo.MarkFailureWithCooldown(d.ID, 429, "limited", &until, &reason); err != nil {
		t.Fatalf("MarkFailureWithCooldown: %v", err)
	}
	type snap struct {
		BaseURL, EncKey, CooldownUntil, CooldownReason, LastFailedAt string
		Enabled                                                      int
		ArchivedAt                                                   sqlNullString
	}
	read := func() snap {
		t.Helper()
		var s snap
		var coolU, coolR, failed, archived sqlNullString
		if err := st.DB().QueryRow(`
			SELECT base_url, encrypted_key, COALESCE(cooldown_until,''), COALESCE(cooldown_reason,''),
				COALESCE(last_failed_at,''), enabled, archived_at
			FROM provider_keys WHERE id = ?`, d.ID).Scan(
			&s.BaseURL, &s.EncKey, &coolU, &coolR, &failed, &s.Enabled, &archived,
		); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		s.CooldownUntil, s.CooldownReason, s.LastFailedAt = coolU.s, coolR.s, failed.s
		s.ArchivedAt = archived
		return s
	}
	before := read()
	if before.CooldownUntil == "" || before.LastFailedAt == "" {
		t.Fatalf("expected routing state set: %+v", before)
	}

	if err := repo.UpdateEndpointQuota(d.ID, providers.EndpointQuotaInput{
		Mode:    providers.QuotaSeparateCredentials,
		Flow:    providers.QuotaFlowGrok2APIAdmin,
		BaseURL: "https://grok2api-new.example/",
	}); err != nil {
		t.Fatalf("UpdateEndpointQuota: %v", err)
	}
	if err := repo.RotateEndpointQuotaKey(d.ID, "g2a-mut-admin-key-22222222"); err != nil {
		t.Fatalf("RotateEndpointQuotaKey: %v", err)
	}
	if err := repo.UpdateEndpointQuota(d.ID, providers.EndpointQuotaInput{
		Mode: providers.QuotaDisabled,
		Flow: providers.QuotaFlowGrok2APIAdmin,
	}); err != nil {
		t.Fatalf("UpdateEndpointQuota disabled: %v", err)
	}
	after := read()
	if after.BaseURL != before.BaseURL || after.EncKey != before.EncKey {
		t.Fatal("inference base_url or encrypted_key changed by quota mutation")
	}
	if after.CooldownUntil != before.CooldownUntil || after.CooldownReason != before.CooldownReason {
		t.Fatal("cooldown changed by quota mutation")
	}
	if after.LastFailedAt != before.LastFailedAt {
		t.Fatal("last_failed_at changed by quota mutation")
	}
	if after.Enabled != before.Enabled || after.ArchivedAt.valid != before.ArchivedAt.valid {
		t.Fatal("enabled/archived changed by quota mutation")
	}
}

func TestAddEndpoint_AppliesDefaultQuotaConfig(t *testing.T) {
	t.Parallel()
	repo, _, _ := openKeyRepo(t)
	g, err := repo.AddEndpoint(providers.ProviderGrok, "g-def", "https://api.x.ai/v1", "xai-def-key-111111111111")
	if err != nil {
		t.Fatalf("AddEndpoint grok: %v", err)
	}
	if g.QuotaMode != providers.QuotaDisabled || g.QuotaFlow != providers.QuotaFlowGrok2APIAdmin {
		t.Fatalf("grok defaults: mode=%q flow=%q", g.QuotaMode, g.QuotaFlow)
	}
	tv, err := repo.AddEndpoint(providers.ProviderTavily, "t-def", "https://api.tavily.com", "tvly-def-key-1111111111")
	if err != nil {
		t.Fatalf("AddEndpoint tavily: %v", err)
	}
	if tv.QuotaMode != providers.QuotaEndpointCredentials || tv.QuotaFlow != providers.QuotaFlowTavilyUsage {
		t.Fatalf("tavily defaults: mode=%q flow=%q", tv.QuotaMode, tv.QuotaFlow)
	}
}
