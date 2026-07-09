package proxy_test

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-guda-gateway/internal/cooldown"
	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/proxy"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/store"
)

func openProxyTarget(t *testing.T, provider string, keys ...string) (*proxy.Proxy, *providers.KeyRepo, *store.Store) {
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
	repo := providers.NewKeyRepo(st.DB(), mk)
	for i, k := range keys {
		name := string(rune('a' + i))
		if _, err := repo.Add(provider, name, k); err != nil {
			t.Fatalf("Add key %s: %v", name, err)
		}
	}
	px := proxy.New(proxy.Options{Client: http.DefaultClient})
	px.SetCooldownSettings(cooldown.DefaultSettings())
	return px, repo, st
}

func TestForwardMapsPathAndReplacesAuthorization(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	px, repo, _ := openProxyTarget(t, providers.ProviderTavily, "upstream-key")

	req := httptest.NewRequest(http.MethodPost, "/tavily/search?debug=1", strings.NewReader(`{"query":"go"}`))
	req.Header.Set("Authorization", "Bearer inbound")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	result := px.Forward(rec, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/search",
		Provider: providers.ProviderTavily,
		Keys:     repo,
	})

	if result.Err != nil {
		t.Fatalf("Forward returned error: %v", result.Err)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if gotPath != "/search?debug=1" {
		t.Fatalf("path = %q, want /search?debug=1", gotPath)
	}
	if gotAuth != "Bearer upstream-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotBody != `{"query":"go"}` {
		t.Fatalf("body = %q", gotBody)
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Fatalf("response body = %q", rec.Body.String())
	}
}

func TestProxy_RetriesAcrossKeysOn429(t *testing.T) {
	var attempts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, r.Header.Get("Authorization"))
		if len(attempts) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	px, repo, st := openProxyTarget(t, providers.ProviderFirecrawl, "first", "second")

	req := httptest.NewRequest(http.MethodPost, "/firecrawl/scrape", strings.NewReader(`{"url":"https://example.com"}`))
	rec := httptest.NewRecorder()
	result := px.Forward(rec, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/scrape",
		Provider: providers.ProviderFirecrawl,
		Keys:     repo,
	})

	if result.Err != nil {
		t.Fatalf("Forward returned error: %v", result.Err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	want := []string{"Bearer first", "Bearer second"}
	if len(attempts) != len(want) {
		t.Fatalf("attempts = %#v, want %#v", attempts, want)
	}
	for i := range want {
		if attempts[i] != want[i] {
			t.Fatalf("attempt %d auth = %q, want %q", i, attempts[i], want[i])
		}
	}
	var cooldownUntil sql.NullString
	_ = st.DB().QueryRow(`SELECT cooldown_until FROM provider_keys WHERE name = 'a'`).Scan(&cooldownUntil)
	if !cooldownUntil.Valid || cooldownUntil.String == "" {
		t.Fatal("expected key a to have cooldown_until set after 429")
	}
}

func TestProxy_RetriesOn503(t *testing.T) {
	var attempts int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	px, repo, _ := openProxyTarget(t, providers.ProviderGrok, "k1", "k2")
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()
	_ = px.Forward(rec, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/models",
		Provider: providers.ProviderGrok,
		Keys:     repo,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestProxy_CredentialErrorCoolsLongRetriesOtherKey(t *testing.T) {
	var attempts []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("bad key"))
	}))
	defer upstream.Close()

	px, repo, st := openProxyTarget(t, providers.ProviderGrok, "bad-a", "bad-b")
	s := cooldown.DefaultSettings()
	s.MaxRetries = 2
	px.SetCooldownSettings(s)
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()
	_ = px.Forward(rec, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/models",
		Provider: providers.ProviderGrok,
		Keys:     repo,
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2 (retry different key after credential cooldown on first)", len(attempts))
	}
	var reason sql.NullString
	var enabled int
	_ = st.DB().QueryRow(`SELECT cooldown_reason, enabled FROM provider_keys WHERE name = 'a'`).Scan(&reason, &enabled)
	if !reason.Valid || reason.String != "credential_error" {
		t.Fatalf("cooldown_reason = %v, want credential_error", reason)
	}
	if enabled != 1 {
		t.Fatalf("enabled = %d, want 1 (no auto-disable on credential error)", enabled)
	}
}

func TestProxy_RespectsRetryAfter(t *testing.T) {
	before := time.Now()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	px, repo, st := openProxyTarget(t, providers.ProviderTavily, "only")
	req := httptest.NewRequest(http.MethodPost, "/search", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	_ = px.Forward(rec, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/search",
		Provider: providers.ProviderTavily,
		Keys:     repo,
	})
	var untilStr string
	_ = st.DB().QueryRow(`SELECT cooldown_until FROM provider_keys WHERE name = 'a'`).Scan(&untilStr)
	until, err := time.Parse(time.RFC3339Nano, untilStr)
	if err != nil {
		t.Fatalf("parse cooldown_until: %v", err)
	}
	delta := until.Sub(before)
	if delta < 4*time.Second || delta > 7*time.Second {
		t.Fatalf("cooldown delta = %v, want ~5s from Retry-After", delta)
	}
}

func TestProxy_TavilyPlanLimitMapsToClearGatewayError(t *testing.T) {
	upstreamBody := `{"detail":{"error":"This request exceeds your plan's set usage limit. Please upgrade your plan or contact support@tavily.com"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(432)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer upstream.Close()

	px, repo, st := openProxyTarget(t, providers.ProviderTavily, "only")
	req := httptest.NewRequest(http.MethodPost, "/tavily/map", strings.NewReader(`{"url":"https://example.com"}`))
	rec := httptest.NewRecorder()
	_ = px.Forward(rec, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/map",
		Provider: providers.ProviderTavily,
		Keys:     repo,
	})

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"tavily_plan_limit_exceeded"`) ||
		!strings.Contains(body, `"message":"Tavily plan usage limit exceeded"`) {
		t.Fatalf("body = %q, want stable Tavily plan-limit error", body)
	}
	var status int
	var reason sql.NullString
	var cooldownUntil sql.NullString
	var lastMessage string
	_ = st.DB().QueryRow(`
		SELECT last_error_status, cooldown_reason, cooldown_until, last_error_message_redacted
		FROM provider_keys WHERE name = 'a'`,
	).Scan(&status, &reason, &cooldownUntil, &lastMessage)
	if status != 432 {
		t.Fatalf("last_error_status = %d, want upstream 432", status)
	}
	if !reason.Valid || reason.String != "plan_limit_exceeded" {
		t.Fatalf("cooldown_reason = %v, want plan_limit_exceeded", reason)
	}
	if !cooldownUntil.Valid || cooldownUntil.String == "" {
		t.Fatal("expected cooldown_until set after Tavily plan limit")
	}
	if !strings.Contains(lastMessage, "usage limit") {
		t.Fatalf("last_error_message_redacted = %q, want usage limit detail", lastMessage)
	}
}

func TestProxy_MaxRetriesExhausted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("limited"))
	}))
	defer upstream.Close()

	px, repo, st := openProxyTarget(t, providers.ProviderGrok, "k1", "k2", "k3", "k4")
	s := cooldown.DefaultSettings()
	s.MaxRetries = 3
	px.SetCooldownSettings(s)

	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()
	_ = px.Forward(rec, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/models",
		Provider: providers.ProviderGrok,
		Keys:     repo,
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	var cooled int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM provider_keys WHERE cooldown_until IS NOT NULL`).Scan(&cooled)
	if cooled != 3 {
		t.Fatalf("cooled keys = %d, want 3", cooled)
	}
}

func TestProxy_NoKeysConfigured(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	px, repo, _ := openProxyTarget(t, providers.ProviderGrok)
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()
	result := px.Forward(rec, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/models",
		Provider: providers.ProviderGrok,
		Keys:     repo,
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if result.Err == nil {
		t.Fatal("expected error")
	}
}

func TestProxy_MarkSuccessOn2xx(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	px, repo, st := openProxyTarget(t, providers.ProviderGrok, "good")
	req := httptest.NewRequest(http.MethodGet, "/models", nil)
	rec := httptest.NewRecorder()
	_ = px.Forward(rec, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/models",
		Provider: providers.ProviderGrok,
		Keys:     repo,
	})
	var successAt sql.NullString
	var consec int
	_ = st.DB().QueryRow(`SELECT last_success_at, consecutive_failures FROM provider_keys WHERE name = 'a'`).Scan(&successAt, &consec)
	if !successAt.Valid {
		t.Fatal("expected last_success_at set")
	}
	if consec != 0 {
		t.Fatalf("consecutive_failures = %d, want 0", consec)
	}
}

// fakeAttemptRecorder captures attempt rows for proxy instrumentation tests.
type fakeAttemptRecorder struct {
	enabled bool
	rows    []proxy.AttemptLog
	err     error
}

func (f *fakeAttemptRecorder) Enabled() bool { return f.enabled }
func (f *fakeAttemptRecorder) Record(row proxy.AttemptLog) error {
	f.rows = append(f.rows, row)
	return f.err
}

func openProxyWithRecorder(t *testing.T, rec *fakeAttemptRecorder, provider string, keys ...string) (*proxy.Proxy, *providers.KeyRepo) {
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
	repo := providers.NewKeyRepo(st.DB(), mk)
	for i, k := range keys {
		name := string(rune('a' + i))
		if _, err := repo.Add(provider, name, k); err != nil {
			t.Fatalf("Add key %s: %v", name, err)
		}
	}
	px := proxy.New(proxy.Options{Client: http.DefaultClient, AttemptRecorder: rec})
	px.SetCooldownSettings(cooldown.DefaultSettings())
	return px, repo
}

func TestProxy_DisabledRecorderProducesZeroRows(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	rec := &fakeAttemptRecorder{enabled: false}
	px, repo := openProxyWithRecorder(t, rec, providers.ProviderTavily, "k1")
	req := httptest.NewRequest(http.MethodPost, "/tavily/search", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	result := px.Forward(rr, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/search",
		Provider: providers.ProviderTavily,
		Keys:     repo,
	})
	if result.Err != nil {
		t.Fatalf("Forward error: %v", result.Err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(rec.rows) != 0 {
		t.Fatalf("rows = %#v, want none when recorder disabled", rec.rows)
	}
}

func TestProxy_EnabledRecorderLogsTavily432Then200(t *testing.T) {
	var n int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(432)
			_, _ = w.Write([]byte(`{"detail":"plan limit"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	rec := &fakeAttemptRecorder{enabled: true}
	px, repo := openProxyWithRecorder(t, rec, providers.ProviderTavily, "first", "second")
	req := httptest.NewRequest(http.MethodPost, "/tavily/extract", strings.NewReader(`{}`))
	req.Header.Set("X-Request-ID", "req-432-retry")
	rr := httptest.NewRecorder()
	result := px.Forward(rr, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/extract",
		Provider: providers.ProviderTavily,
		Keys:     repo,
	})
	if result.Err != nil {
		t.Fatalf("Forward error: %v", result.Err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(rec.rows) != 2 {
		t.Fatalf("rows = %#v, want 2", rec.rows)
	}

	first := rec.rows[0]
	if first.RequestID != "req-432-retry" {
		t.Fatalf("first.RequestID = %q", first.RequestID)
	}
	if first.Provider != providers.ProviderTavily || first.RouteFamily != "tavily" || first.Path != "/tavily/extract" {
		t.Fatalf("first identity = provider=%q family=%q path=%q", first.Provider, first.RouteFamily, first.Path)
	}
	if first.AttemptIndex != 1 {
		t.Fatalf("first.AttemptIndex = %d, want 1", first.AttemptIndex)
	}
	if first.ProviderKeyID == nil {
		t.Fatal("first.ProviderKeyID is nil")
	}
	if first.UpstreamStatus == nil || *first.UpstreamStatus != 432 {
		t.Fatalf("first.UpstreamStatus = %v, want 432", first.UpstreamStatus)
	}
	if first.StatusClass != "4xx" {
		t.Fatalf("first.StatusClass = %q, want 4xx", first.StatusClass)
	}
	if first.Reason == nil || *first.Reason != "plan_limit_exceeded" {
		t.Fatalf("first.Reason = %v, want plan_limit_exceeded", first.Reason)
	}
	if first.CooldownUntil == nil || *first.CooldownUntil == "" {
		t.Fatal("first.CooldownUntil expected set")
	}
	if first.Terminal {
		t.Fatal("first.Terminal = true, want false (retrying)")
	}

	second := rec.rows[1]
	if second.AttemptIndex != 2 {
		t.Fatalf("second.AttemptIndex = %d, want 2", second.AttemptIndex)
	}
	if second.UpstreamStatus == nil || *second.UpstreamStatus != 200 {
		t.Fatalf("second.UpstreamStatus = %v, want 200", second.UpstreamStatus)
	}
	if second.StatusClass != "2xx" {
		t.Fatalf("second.StatusClass = %q, want 2xx", second.StatusClass)
	}
	if !second.Terminal {
		t.Fatal("second.Terminal = false, want true")
	}
}

func TestProxy_RecorderErrorDoesNotChangeSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	rec := &fakeAttemptRecorder{enabled: true, err: errors.New("disk full")}
	px, repo := openProxyWithRecorder(t, rec, providers.ProviderTavily, "k1")
	req := httptest.NewRequest(http.MethodPost, "/tavily/search", strings.NewReader(`{"q":"x"}`))
	rr := httptest.NewRecorder()
	result := px.Forward(rr, req, proxy.Target{
		BaseURL:  upstream.URL,
		Path:     "/search",
		Provider: providers.ProviderTavily,
		Keys:     repo,
	})
	if result.Err != nil {
		t.Fatalf("Forward error: %v", result.Err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if rr.Body.String() != `{"ok":true}` {
		t.Fatalf("body = %q, want unchanged success body", rr.Body.String())
	}
	// Record was still invoked (error swallowed).
	if len(rec.rows) != 1 {
		t.Fatalf("rows = %#v, want 1 recorded despite error", rec.rows)
	}
}
