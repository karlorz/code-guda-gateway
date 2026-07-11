package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/store"
)

func openQuotaTestDB(t *testing.T) (*store.Store, []byte, *KeyRepo, *SettingsRepo) {
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
	return st, mk, NewKeyRepo(st.DB(), mk), NewSettingsRepo(st.DB())
}

// openQuotaRefreshStore wraps openQuotaTestDB in the (keyRepo, st, mk) order used by per-key refresh tests.
func openQuotaRefreshStore(t *testing.T) (*KeyRepo, *store.Store, []byte) {
	t.Helper()
	st, mk, keyRepo, _ := openQuotaTestDB(t)
	return keyRepo, st, mk
}

func TestParseTavilyUsageQuota(t *testing.T) {
	body := []byte(`{
	  "key": {
	    "usage": 150,
	    "limit": 1000,
	    "search_usage": 100,
	    "extract_usage": 25,
	    "crawl_usage": 15,
	    "map_usage": 7,
	    "research_usage": 3
	  },
	  "account": {
	    "current_plan": "Bootstrap",
	    "plan_usage": 500,
	    "plan_limit": 15000,
	    "paygo_usage": 25,
	    "paygo_limit": 100
	  }
	}`)
	var payload tavilyUsageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	q := normalizeTavilyUsage(ProviderTavily, nil, "2026-07-08T00:00:00Z", "2026-07-08T00:05:00Z", payload)
	if !q.Available {
		t.Fatal("quota should be available")
	}
	if q.Source != "tavily_usage" {
		t.Fatalf("source = %q", q.Source)
	}
	if q.Used == nil || *q.Used != 150 {
		t.Fatalf("used = %#v", q.Used)
	}
	if q.LimitValue == nil || *q.LimitValue != 1000 {
		t.Fatalf("limit = %#v", q.LimitValue)
	}
	if q.Remaining == nil || *q.Remaining != 850 {
		t.Fatalf("remaining = %#v", q.Remaining)
	}
	if q.Details["remaining_basis"] != "key" {
		t.Fatalf("remaining_basis = %#v, want key", q.Details["remaining_basis"])
	}
}

func TestParseTavilyUsageQuota_NoKeyLimitFallsBackToAccountPlan(t *testing.T) {
	// Real Tavily responses often omit key.limit (or send null) while still
	// reporting account plan_usage/plan_limit. Without a remaining value the
	// pool summary cannot show Known remaining.
	body := []byte(`{
	  "key": {
	    "usage": 42,
	    "search_usage": 40,
	    "extract_usage": 2
	  },
	  "account": {
	    "current_plan": "Researcher",
	    "plan_usage": 1200,
	    "plan_limit": 5000,
	    "paygo_usage": 0,
	    "paygo_limit": 0
	  }
	}`)
	var payload tavilyUsageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	q := normalizeTavilyUsage(ProviderTavily, nil, "2026-07-08T00:00:00Z", "2026-07-08T00:05:00Z", payload)
	if !q.Available {
		t.Fatal("quota should be available")
	}
	if q.Used == nil || *q.Used != 42 {
		t.Fatalf("used = %#v (prefer key.usage for used)", q.Used)
	}
	if q.LimitValue == nil || *q.LimitValue != 5000 {
		t.Fatalf("limit = %#v, want account plan_limit", q.LimitValue)
	}
	if q.Remaining == nil || *q.Remaining != 3800 {
		t.Fatalf("remaining = %#v, want plan_limit - plan_usage", q.Remaining)
	}
	if q.Details["remaining_basis"] != "account_plan" {
		t.Fatalf("remaining_basis = %#v", q.Details["remaining_basis"])
	}
}

func TestParseTavilyUsageQuota_UsageOnlyNoLimitNoRemaining(t *testing.T) {
	body := []byte(`{
	  "key": {"usage": 10},
	  "account": {"current_plan": "Free"}
	}`)
	var payload tavilyUsageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	q := normalizeTavilyUsage(ProviderTavily, nil, "2026-07-08T00:00:00Z", "2026-07-08T00:05:00Z", payload)
	if !q.Available {
		t.Fatal("quota should still be available (refresh succeeded)")
	}
	if q.Used == nil || *q.Used != 10 {
		t.Fatalf("used = %#v", q.Used)
	}
	if q.Remaining != nil {
		t.Fatalf("remaining = %#v, want nil when no key.limit and no plan_limit", q.Remaining)
	}
	if q.LimitValue != nil {
		t.Fatalf("limit = %#v, want nil", q.LimitValue)
	}
}

func TestParseTavilyUsageQuota_ClampsNegativeRemaining(t *testing.T) {
	body := []byte(`{"key":{"usage":120,"limit":100},"account":{}}`)
	var payload tavilyUsageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	q := normalizeTavilyUsage(ProviderTavily, nil, "2026-07-08T00:00:00Z", "2026-07-08T00:05:00Z", payload)
	if q.Remaining == nil || *q.Remaining != 0 {
		t.Fatalf("remaining = %#v, want 0 when usage exceeds limit", q.Remaining)
	}
}

func TestParseFirecrawlCreditUsageV2(t *testing.T) {
	body := []byte(`{
	  "success": true,
	  "data": {
	    "remainingCredits": 7500,
	    "planCredits": 10000,
	    "billingPeriodStart": "2026-07-01T00:00:00Z",
	    "billingPeriodEnd": "2026-08-01T00:00:00Z"
	  }
	}`)
	var payload firecrawlCreditUsageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	q := normalizeFirecrawlCreditUsage(ProviderFirecrawl, nil, "2026-07-08T00:00:00Z", "2026-07-08T00:05:00Z", payload)
	if !q.Available {
		t.Fatal("quota should be available")
	}
	if q.Source != "firecrawl_credit_usage" {
		t.Fatalf("source = %q", q.Source)
	}
	if q.LimitValue == nil || *q.LimitValue != 10000 {
		t.Fatalf("limit = %#v", q.LimitValue)
	}
	if q.Remaining == nil || *q.Remaining != 7500 {
		t.Fatalf("remaining = %#v", q.Remaining)
	}
	if q.Used == nil || *q.Used != 2500 {
		t.Fatalf("used = %#v", q.Used)
	}
}

func TestParseFirecrawlCreditUsagePlanPlusOneTimePack(t *testing.T) {
	// Matches dashboard: 1000 free plan + 400 one-time = 1400 remaining; API planCredits=1000 only.
	body := []byte(`{
	  "success": true,
	  "data": {
	    "remainingCredits": 1400,
	    "planCredits": 1000,
	    "billingPeriodStart": "2026-07-07T12:07:29.939Z",
	    "billingPeriodEnd": "2026-08-07T12:07:29.939Z"
	  }
	}`)
	var payload firecrawlCreditUsageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	q := normalizeFirecrawlCreditUsage(ProviderFirecrawl, nil, "2026-07-08T00:00:00Z", "2026-07-08T00:05:00Z", payload)
	if !q.Available {
		t.Fatal("quota should be available")
	}
	if q.Remaining == nil || *q.Remaining != 1400 {
		t.Fatalf("remaining = %#v", q.Remaining)
	}
	if q.LimitValue != nil {
		t.Fatalf("limit should be omitted when remaining > planCredits, got %#v", q.LimitValue)
	}
	if q.Used == nil || *q.Used != 0 {
		t.Fatalf("used = %#v, want 0", q.Used)
	}
	if q.Details == nil || q.Details["plan_credits"] != int64(1000) {
		t.Fatalf("details = %#v", q.Details)
	}
	if q.Details["extra_credits_remaining"] != int64(400) {
		t.Fatalf("extra = %#v", q.Details["extra_credits_remaining"])
	}
}

func TestParseFirecrawlCreditUsagePlanPlusOneTimeNegativeDerivedUsed(t *testing.T) {
	body := []byte(`{
	  "success": true,
	  "data": {
	    "remainingCredits": 1400,
	    "planCredits": 1000
	  }
	}`)
	var payload firecrawlCreditUsageResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	q := normalizeFirecrawlCreditUsage(ProviderFirecrawl, nil, "2026-07-08T00:00:00Z", "2026-07-08T00:05:00Z", payload)
	if q.Used == nil || *q.Used != 0 {
		t.Fatalf("used = %#v, want 0 when derived used would be -400", q.Used)
	}
}

func TestNormalizeGrok2APITokensAggregatesQuotaModes(t *testing.T) {
	body := []byte(`{
	  "tokens": [
	    {"token": "sso-one", "quota": {"fast": {"remaining": 8, "total": 10}, "expert": {"remaining": 2, "total": 5}}},
	    {"token": "sso-two", "quota": {"fast": {"remaining": 4, "total": 10}, "heavy": {"remaining": 1, "total": 2}}}
	  ]
	}`)
	var payload grok2APITokensResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	q := normalizeGrok2APITokens(ProviderGrok, nil, "2026-07-08T00:00:00Z", "2026-07-08T00:05:00Z", payload)
	if !q.Available {
		t.Fatal("quota should be available")
	}
	if q.Source != "grok2api_admin_tokens" {
		t.Fatalf("source = %q", q.Source)
	}
	if q.Remaining == nil || *q.Remaining != 15 {
		t.Fatalf("remaining = %#v", q.Remaining)
	}
	if q.LimitValue == nil || *q.LimitValue != 27 {
		t.Fatalf("limit = %#v", q.LimitValue)
	}
	if q.Used == nil || *q.Used != 12 {
		t.Fatalf("used = %#v", q.Used)
	}
}

func TestQuotaRefresherTavilySendsBearerAndParsesUsage(t *testing.T) {
	const testKey = "tvly-test-key-abcdefghij"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":{"usage":10,"limit":100},"account":{}}`))
	}))
	defer srv.Close()

	_, _, keyRepo, settingsRepo := openQuotaTestDB(t)
	_, err := keyRepo.AddEndpoint(ProviderTavily, "t1", srv.URL, testKey)
	if err != nil {
		t.Fatal(err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.Refresh(context.Background(), ProviderTavily)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+testKey {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !q.Available || q.Source != "tavily_usage" {
		t.Fatalf("quota = %+v", q)
	}
	if q.Remaining == nil || *q.Remaining != 90 {
		t.Fatalf("remaining = %#v", q.Remaining)
	}
}

func TestQuotaRefresherFirecrawlSendsBearerAndParsesCreditUsage(t *testing.T) {
	const testKey = "fc-test-key-abcdefghijklmnop"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/team/credit-usage" && r.URL.Path != "/team/credit-usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"remainingCredits":50,"planCredits":100}}`))
	}))
	defer srv.Close()

	_, _, keyRepo, settingsRepo := openQuotaTestDB(t)
	base := strings.TrimSuffix(srv.URL, "/") + "/v2"
	_, err := keyRepo.AddEndpoint(ProviderFirecrawl, "f1", base, testKey)
	if err != nil {
		t.Fatal(err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.Refresh(context.Background(), ProviderFirecrawl)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+testKey {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !q.Available || q.Source != "firecrawl_credit_usage" {
		t.Fatalf("quota = %+v", q)
	}
}

func TestQuotaRefresherUnauthorizedRedactsKey(t *testing.T) {
	const testKey = "tvly-secret-key-zzzzzzzz"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid key "+testKey, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, _, keyRepo, settingsRepo := openQuotaTestDB(t)
	_, err := keyRepo.AddEndpoint(ProviderTavily, "t1", srv.URL, testKey)
	if err != nil {
		t.Fatal(err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.Refresh(context.Background(), ProviderTavily)
	if err != nil {
		t.Fatal(err)
	}
	if q.Available {
		t.Fatal("expected unavailable")
	}
	if q.MessageRedacted == nil {
		t.Fatal("missing message")
	}
	msg := *q.MessageRedacted
	if strings.Contains(msg, testKey) || strings.Contains(msg, "tvly-") || strings.Contains(strings.ToLower(msg), "bearer") {
		t.Fatalf("message leaked credential: %q", msg)
	}
}

func TestQuotaRefresherInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{not json`)
	}))
	defer srv.Close()

	_, _, keyRepo, settingsRepo := openQuotaTestDB(t)
	_, err := keyRepo.AddEndpoint(ProviderTavily, "t1", srv.URL, "tvly-test-key-abcdefghij")
	if err != nil {
		t.Fatal(err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.Refresh(context.Background(), ProviderTavily)
	if err != nil {
		t.Fatal(err)
	}
	if q.Available {
		t.Fatal("expected unavailable")
	}
	if q.MessageRedacted == nil || !strings.Contains(*q.MessageRedacted, "not understood") {
		t.Fatalf("message = %#v", q.MessageRedacted)
	}
}

func TestQuotaRefresherGrokAdminRequired(t *testing.T) {
	_, _, keyRepo, settingsRepo := openQuotaTestDB(t)
	ref := &QuotaRefresher{
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.Refresh(context.Background(), ProviderGrok)
	if err != nil {
		t.Fatal(err)
	}
	if q.Available || q.Source != "grok2api_admin_required" {
		t.Fatalf("quota = %+v", q)
	}
}

func TestQuotaRefresherGrok2API(t *testing.T) {
	_, mk, keyRepo, settingsRepo := openQuotaTestDB(t)

	var batchCalled bool
	var tokensCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer secret-admin-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/admin/api/batch/refresh" {
			if r.URL.Query().Get("all_manageable") != "true" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// batch/refresh requires a JSON body {"tokens":[]}
			rb, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(rb), `"tokens"`) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			batchCalled = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/admin/api/tokens" {
			tokensCalled = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"tokens": [
					{"token": "t1", "quota": {"fast": {"remaining": 8, "total": 10}, "expert": {"remaining": 2, "total": 5}}}
				]
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}

	// Case 1: mode is grok2api_admin but missing admin key
	if err := settingsRepo.SetGrokQuotaMode("grok2api_admin"); err != nil {
		t.Fatal(err)
	}
	if err := settingsRepo.SetGrok2APIAdminBaseURL(srv.URL); err != nil {
		t.Fatal(err)
	}
	q, err := ref.Refresh(context.Background(), ProviderGrok)
	if err != nil {
		t.Fatal(err)
	}
	if q.Available || q.Source != "grok2api_admin_required" {
		t.Fatalf("case 1 failed: %+v", q)
	}

	// Case 2: mode is grok2api_admin and valid admin key exists
	if err := settingsRepo.SetGrok2APIAdminKey(mk, "secret-admin-key"); err != nil {
		t.Fatal(err)
	}
	q, err = ref.Refresh(context.Background(), ProviderGrok)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Available || q.Source != "grok2api_admin_tokens" {
		t.Fatalf("case 2 failed: %+v", q)
	}
	if !batchCalled || !tokensCalled {
		t.Fatalf("expected both API calls: batchCalled=%v, tokensCalled=%v", batchCalled, tokensCalled)
	}
	if q.Remaining == nil || *q.Remaining != 10 {
		t.Fatalf("expected remaining=10, got %+v", q.Remaining)
	}
	if q.LimitValue == nil || *q.LimitValue != 15 {
		t.Fatalf("expected limit=15, got %+v", q.LimitValue)
	}

	// Case 3: invalid admin key
	if err := settingsRepo.SetGrok2APIAdminKey(mk, "wrong-key"); err != nil {
		t.Fatal(err)
	}
	q, err = ref.Refresh(context.Background(), ProviderGrok)
	if err != nil {
		t.Fatal(err)
	}
	if q.Available {
		t.Fatalf("expected unauthorized failure: %+v", q)
	}
	if q.MessageRedacted == nil || !strings.Contains(*q.MessageRedacted, "rejected") {
		t.Fatalf("expected redacted rejection message, got: %+v", q.MessageRedacted)
	}
}

func TestQuotaRefresherGrok2APIBatchFailureTolerated(t *testing.T) {
	// When /admin/api/batch/refresh fails (timeout/400), the refresher should
	// still read /admin/api/tokens and return cached quota rather than failing.
	_, mk, keyRepo, settingsRepo := openQuotaTestDB(t)
	if err := settingsRepo.SetGrokQuotaMode("grok2api_admin"); err != nil {
		t.Fatal(err)
	}
	if err := settingsRepo.SetGrok2APIAdminKey(mk, "secret-admin-key"); err != nil {
		t.Fatal(err)
	}

	batchHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer secret-admin-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/admin/api/batch/refresh" {
			batchHits++
			// Simulate upstream timeout / failure
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/admin/api/tokens" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"tokens": [
					{"token": "t1", "quota": {"fast": {"remaining": 5, "total": 10}}}
				]
			}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if err := settingsRepo.SetGrok2APIAdminBaseURL(srv.URL); err != nil {
		t.Fatal(err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.Refresh(context.Background(), ProviderGrok)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if batchHits == 0 {
		t.Fatal("expected batch/refresh to be attempted")
	}
	if !q.Available || q.Source != "grok2api_admin_tokens" {
		t.Fatalf("expected available cached quota despite batch failure: %+v", q)
	}
	if q.Remaining == nil || *q.Remaining != 5 {
		t.Fatalf("expected remaining=5, got %+v", q.Remaining)
	}
}

func TestQuotaRefresher_RefreshKeyTavilyCachesPerKey(t *testing.T) {
	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/usage" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"key":{"usage":25,"limit":100},"account":{"plan_usage":25,"plan_limit":100}}`))
	}))
	defer ts.Close()

	keyRepo, st, mk := openQuotaRefreshStore(t)
	settings := NewSettingsRepo(st.DB())
	key, err := keyRepo.AddEndpoint(ProviderTavily, "t1", ts.URL, "tvly-secret-1")
	if err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	refresher := QuotaRefresher{
		ProviderKeys: keyRepo,
		Settings:     settings,
		Quotas:       NewQuotaRepo(st.DB()),
		KeyQuotas:    NewKeyQuotaRepo(st.DB()),
		MasterKey:    mk,
	}
	q, err := refresher.RefreshKey(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("RefreshKey: %v", err)
	}
	if !q.Available || q.ProviderKeyID != key.ID || q.Remaining == nil || *q.Remaining != 75 {
		t.Fatalf("quota = %#v", q)
	}
	if auth != "Bearer tvly-secret-1" {
		t.Fatalf("auth = %q", auth)
	}
}

func TestQuotaRefresher_RefreshAllSkipsCooling(t *testing.T) {
	keyRepo, st, mk := openQuotaRefreshStore(t)
	k1, _ := keyRepo.Add(ProviderTavily, "a", "tvly-a")
	k2, _ := keyRepo.Add(ProviderTavily, "b", "tvly-b")
	until := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	_, _ = st.DB().Exec(`UPDATE provider_keys SET cooldown_until = ?, cooldown_reason = ? WHERE id = ?`, until, "plan_limit_exceeded", k2.ID)
	refresher := QuotaRefresher{
		ProviderKeys: keyRepo,
		Settings:     NewSettingsRepo(st.DB()),
		KeyQuotas:    NewKeyQuotaRepo(st.DB()),
		MasterKey:    mk,
	}
	result, err := refresher.RefreshAllKeys(context.Background(), ProviderTavily)
	if err != nil {
		t.Fatalf("RefreshAllKeys: %v", err)
	}
	if result.Attempted != 1 || result.SkippedCooldown != 1 || result.KeyResults[0].ProviderKeyID != k1.ID {
		t.Fatalf("result = %#v", result)
	}
}

func TestQuotaRefreshKey_UsesRowOwnedTavilyURLAndKey(t *testing.T) {
	const rowKey = "tvly-row-owned-key-aaaa"
	const wrongKey = "tvly-wrong-provider-key"
	var mu sync.Mutex
	var gotAuth string
	var hitCount int

	rowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hitCount++
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		if r.URL.Path != "/usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":{"usage":5,"limit":50},"account":{}}`))
	}))
	defer rowSrv.Close()

	// Provider-default server must not receive the quota call.
	defaultSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("quota hit provider default URL %s instead of row-owned endpoint", r.URL.String())
		http.Error(w, "wrong host", http.StatusTeapot)
	}))
	defer defaultSrv.Close()

	_, _, keyRepo, settingsRepo := openQuotaTestDB(t)
	if err := settingsRepo.SetBaseURL(ProviderTavily, defaultSrv.URL); err != nil {
		t.Fatal(err)
	}
	key, err := keyRepo.AddEndpoint(ProviderTavily, "row", rowSrv.URL, rowKey)
	if err != nil {
		t.Fatal(err)
	}
	// Seed a decoy row that would be selected if RefreshKey used SelectKey.
	if _, err := keyRepo.AddEndpoint(ProviderTavily, "decoy", defaultSrv.URL, wrongKey); err != nil {
		t.Fatal(err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   rowSrv.Client(),
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.RefreshKey(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("RefreshKey: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hitCount != 1 {
		t.Fatalf("row-owned server hit count = %d, want 1", hitCount)
	}
	if gotAuth != "Bearer "+rowKey {
		t.Fatalf("auth = %q, want Bearer %s", gotAuth, rowKey)
	}
	if !q.Available || q.ProviderKeyID != key.ID {
		t.Fatalf("quota = %#v", q)
	}
	if q.Remaining == nil || *q.Remaining != 45 {
		t.Fatalf("remaining = %#v", q.Remaining)
	}
}

func TestQuotaRefreshAll_UsesEachFirecrawlEndpointPair(t *testing.T) {
	const keyA = "fc-endpoint-a-key-11111111"
	const keyB = "fc-endpoint-b-key-22222222"

	var mu sync.Mutex
	hits := map[string]string{} // server label -> auth bearer key

	mkSrv := func(label string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v2/team/credit-usage" && r.URL.Path != "/team/credit-usage" {
				t.Errorf("%s path = %q", label, r.URL.Path)
			}
			auth := r.Header.Get("Authorization")
			mu.Lock()
			hits[label] = auth
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"remainingCredits":10,"planCredits":20}}`))
		}))
	}
	srvA := mkSrv("a")
	defer srvA.Close()
	srvB := mkSrv("b")
	defer srvB.Close()

	baseA := strings.TrimSuffix(srvA.URL, "/") + "/v2"
	baseB := strings.TrimSuffix(srvB.URL, "/") + "/v2"

	keyRepo, st, mk := openQuotaRefreshStore(t)
	settings := NewSettingsRepo(st.DB())
	// Provider default points at neither endpoint.
	if err := settings.SetBaseURL(ProviderFirecrawl, "https://api.firecrawl.dev/v2"); err != nil {
		t.Fatal(err)
	}
	epA, err := keyRepo.AddEndpoint(ProviderFirecrawl, "a", baseA, keyA)
	if err != nil {
		t.Fatal(err)
	}
	epB, err := keyRepo.AddEndpoint(ProviderFirecrawl, "b", baseB, keyB)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	ref := &QuotaRefresher{
		HTTPClient:   client,
		ProviderKeys: keyRepo,
		Settings:     settings,
		KeyQuotas:    NewKeyQuotaRepo(st.DB()),
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	result, err := ref.RefreshAllKeys(context.Background(), ProviderFirecrawl)
	if err != nil {
		t.Fatalf("RefreshAllKeys: %v", err)
	}
	if result.Attempted != 2 || result.Succeeded != 2 {
		t.Fatalf("result = %#v", result)
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["a"] != "Bearer "+keyA {
		t.Fatalf("server A auth = %q, want Bearer %s (ep id %d)", hits["a"], keyA, epA.ID)
	}
	if hits["b"] != "Bearer "+keyB {
		t.Fatalf("server B auth = %q, want Bearer %s (ep id %d)", hits["b"], keyB, epB.ID)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %#v, want both endpoints", hits)
	}
}

func TestProviderDefaultChange_DoesNotChangeEndpointQuotaURL(t *testing.T) {
	const rowKey = "tvly-stable-endpoint-key"
	var mu sync.Mutex
	var hitCount int

	rowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hitCount++
		mu.Unlock()
		if r.URL.Path != "/usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":{"usage":1,"limit":10},"account":{}}`))
	}))
	defer rowSrv.Close()

	newDefaultSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("quota hit changed provider default %s; row base URL should be sticky", r.URL.String())
		http.Error(w, "wrong host", http.StatusTeapot)
	}))
	defer newDefaultSrv.Close()

	_, _, keyRepo, settingsRepo := openQuotaTestDB(t)
	// Snapshot a different default first; endpoint row stores its own URL.
	if err := settingsRepo.SetBaseURL(ProviderTavily, "https://api.tavily.com"); err != nil {
		t.Fatal(err)
	}
	key, err := keyRepo.AddEndpoint(ProviderTavily, "sticky", rowSrv.URL, rowKey)
	if err != nil {
		t.Fatal(err)
	}
	// Change provider default after the endpoint exists — must not reroute quota.
	if err := settingsRepo.SetBaseURL(ProviderTavily, newDefaultSrv.URL); err != nil {
		t.Fatal(err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   rowSrv.Client(),
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.RefreshKey(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("RefreshKey: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hitCount != 1 {
		t.Fatalf("row server hits = %d, want 1 (provider default must not be used)", hitCount)
	}
	if !q.Available || q.ProviderKeyID != key.ID {
		t.Fatalf("quota = %#v", q)
	}
}

// Compatibility: provider-level Refresh still uses Settings-backed Grok admin credentials.
func TestRefresh_GrokProviderLevelStillUsesSettingsAdmin(t *testing.T) {
	_, mk, keyRepo, settingsRepo := openQuotaTestDB(t)
	if err := settingsRepo.SetGrokQuotaMode("grok2api_admin"); err != nil {
		t.Fatal(err)
	}
	if err := settingsRepo.SetGrok2APIAdminKey(mk, "secret-admin-key"); err != nil {
		t.Fatal(err)
	}

	adminHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		adminHits++
		if r.Header.Get("Authorization") != "Bearer secret-admin-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tokens":[{"token":"t1","quota":{"fast":{"remaining":99,"total":100}}}]}`))
	}))
	defer srv.Close()
	if err := settingsRepo.SetGrok2APIAdminBaseURL(srv.URL); err != nil {
		t.Fatal(err)
	}

	// Default Grok endpoints have quota disabled; provider-level Refresh still works.
	if _, err := keyRepo.AddEndpoint(ProviderGrok, "inf-1", "https://api.x.ai/v1", "xai-inf-key-11111111"); err != nil {
		t.Fatal(err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	pq, err := ref.Refresh(context.Background(), ProviderGrok)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !pq.Available || pq.Source != "grok2api_admin_tokens" {
		t.Fatalf("provider-level quota = %+v", pq)
	}
	if pq.Remaining == nil || *pq.Remaining != 99 {
		t.Fatalf("provider remaining = %#v", pq.Remaining)
	}
	if adminHits == 0 {
		t.Fatal("expected provider-level Refresh to hit settings admin URL")
	}
}

// --- Task 3: per-endpoint quota sidecar refresh ---

func TestQuotaRefreshKey_GrokUsesOwningSeparateQuotaCredentials(t *testing.T) {
	const adminA = "g2a-admin-a-key-aaaaaaaa"
	const adminB = "g2a-admin-b-key-bbbbbbbb"
	const infA = "xai-inf-a-key-1111111111"
	const infB = "xai-inf-b-key-2222222222"

	var mu sync.Mutex
	type hit struct {
		auth string
	}
	hits := map[string][]hit{}

	mkAdminSrv := func(label, wantKey string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			mu.Lock()
			hits[label] = append(hits[label], hit{auth: auth})
			mu.Unlock()
			if auth != "Bearer "+wantKey {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodPost && r.URL.Path == "/admin/api/batch/refresh" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"success":true}`))
				return
			}
			if r.Method == http.MethodGet && r.URL.Path == "/admin/api/tokens" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"tokens":[{"token":"t1","quota":{"fast":{"remaining":7,"total":10}}}]}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
	}
	srvA := mkAdminSrv("a", adminA)
	defer srvA.Close()
	srvB := mkAdminSrv("b", adminB)
	defer srvB.Close()

	settingsHits := 0
	settingsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settingsHits++
		t.Errorf("RefreshKey hit provider-global Grok admin %s", r.URL.String())
		http.Error(w, "wrong host", http.StatusTeapot)
	}))
	defer settingsSrv.Close()

	keyRepo, st, mk := openQuotaRefreshStore(t)
	settings := NewSettingsRepo(st.DB())
	if err := settings.SetGrokQuotaMode("grok2api_admin"); err != nil {
		t.Fatal(err)
	}
	if err := settings.SetGrok2APIAdminBaseURL(settingsSrv.URL); err != nil {
		t.Fatal(err)
	}
	if err := settings.SetGrok2APIAdminKey(mk, "g2a-global-admin-should-not-use"); err != nil {
		t.Fatal(err)
	}

	epA, err := keyRepo.AddEndpointWithQuota(ProviderGrok, "a", "https://new-api-a.example/v1", infA, EndpointQuotaInput{
		Mode: QuotaSeparateCredentials, Flow: QuotaFlowGrok2APIAdmin,
		BaseURL: srvA.URL, RawKey: adminA,
	})
	if err != nil {
		t.Fatalf("add a: %v", err)
	}
	epB, err := keyRepo.AddEndpointWithQuota(ProviderGrok, "b", "https://new-api-b.example/v1", infB, EndpointQuotaInput{
		Mode: QuotaSeparateCredentials, Flow: QuotaFlowGrok2APIAdmin,
		BaseURL: srvB.URL, RawKey: adminB,
	})
	if err != nil {
		t.Fatalf("add b: %v", err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
		ProviderKeys: keyRepo,
		Settings:     settings,
		KeyQuotas:    NewKeyQuotaRepo(st.DB()),
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}

	qa, err := ref.RefreshKey(context.Background(), epA.ID)
	if err != nil {
		t.Fatalf("RefreshKey a: %v", err)
	}
	qb, err := ref.RefreshKey(context.Background(), epB.ID)
	if err != nil {
		t.Fatalf("RefreshKey b: %v", err)
	}
	if !qa.Available || qa.ProviderKeyID != epA.ID || qa.Source != "grok2api_admin_tokens" {
		t.Fatalf("quota a = %+v", qa)
	}
	if !qb.Available || qb.ProviderKeyID != epB.ID || qb.Source != "grok2api_admin_tokens" {
		t.Fatalf("quota b = %+v", qb)
	}
	if settingsHits != 0 {
		t.Fatalf("provider-global admin hits = %d, want 0", settingsHits)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(hits["a"]) == 0 {
		t.Fatal("endpoint A admin server received no requests")
	}
	if len(hits["b"]) == 0 {
		t.Fatal("endpoint B admin server received no requests")
	}
	for _, h := range hits["a"] {
		if h.auth != "Bearer "+adminA {
			t.Fatalf("server A saw auth %q, want Bearer %s", h.auth, adminA)
		}
		if h.auth == "Bearer "+adminB || h.auth == "Bearer "+infA || h.auth == "Bearer "+infB {
			t.Fatalf("server A saw cross-row credential: %q", h.auth)
		}
	}
	for _, h := range hits["b"] {
		if h.auth != "Bearer "+adminB {
			t.Fatalf("server B saw auth %q, want Bearer %s", h.auth, adminB)
		}
		if h.auth == "Bearer "+adminA || h.auth == "Bearer "+infA || h.auth == "Bearer "+infB {
			t.Fatalf("server B saw cross-row credential: %q", h.auth)
		}
	}
	if hits["a"][0].auth == hits["b"][0].auth {
		t.Fatal("both endpoints used the same admin credential")
	}
}

func TestQuotaRefreshKey_TavilyUsesEndpointCredentials(t *testing.T) {
	const rowKey = "tvly-endpoint-cred-key-aaaa"
	var gotAuth string
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/usage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":{"usage":3,"limit":30},"account":{}}`))
	}))
	defer srv.Close()

	keyRepo, st, mk := openQuotaRefreshStore(t)
	ep, err := keyRepo.AddEndpointWithQuota(ProviderTavily, "tv", srv.URL, rowKey, EndpointQuotaInput{
		Mode: QuotaEndpointCredentials, Flow: QuotaFlowTavilyUsage,
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     NewSettingsRepo(st.DB()),
		KeyQuotas:    NewKeyQuotaRepo(st.DB()),
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.RefreshKey(context.Background(), ep.ID)
	if err != nil {
		t.Fatalf("RefreshKey: %v", err)
	}
	if hits != 1 {
		t.Fatalf("hits = %d", hits)
	}
	if gotAuth != "Bearer "+rowKey {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !q.Available || q.Remaining == nil || *q.Remaining != 27 {
		t.Fatalf("quota = %+v", q)
	}
}

func TestQuotaRefreshKey_FirecrawlUsesEndpointCredentials(t *testing.T) {
	const rowKey = "fc-endpoint-cred-key-bbbbbb"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"remainingCredits":40,"planCredits":100}}`))
	}))
	defer srv.Close()
	base := strings.TrimSuffix(srv.URL, "/") + "/v2"

	keyRepo, st, mk := openQuotaRefreshStore(t)
	ep, err := keyRepo.AddEndpointWithQuota(ProviderFirecrawl, "fc", base, rowKey, EndpointQuotaInput{
		Mode: QuotaEndpointCredentials, Flow: QuotaFlowFirecrawlCreditUsage,
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     NewSettingsRepo(st.DB()),
		KeyQuotas:    NewKeyQuotaRepo(st.DB()),
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.RefreshKey(context.Background(), ep.ID)
	if err != nil {
		t.Fatalf("RefreshKey: %v", err)
	}
	if gotAuth != "Bearer "+rowKey {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !q.Available || q.Source != "firecrawl_credit_usage" {
		t.Fatalf("quota = %+v", q)
	}
	if q.Remaining == nil || *q.Remaining != 40 {
		t.Fatalf("remaining = %#v", q.Remaining)
	}
}

func TestQuotaRefreshKey_DisabledSkipsHTTP(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	keyRepo, st, mk := openQuotaRefreshStore(t)
	ep, err := keyRepo.AddEndpointWithQuota(ProviderGrok, "off", "https://api.x.ai/v1", "xai-disabled-inf-key-1111", EndpointQuotaInput{
		Mode: QuotaDisabled, Flow: QuotaFlowGrok2APIAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := &QuotaRefresher{
		HTTPClient:   srv.Client(),
		ProviderKeys: keyRepo,
		Settings:     NewSettingsRepo(st.DB()),
		KeyQuotas:    NewKeyQuotaRepo(st.DB()),
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.RefreshKey(context.Background(), ep.ID)
	if err != nil {
		t.Fatalf("RefreshKey: %v", err)
	}
	if hits != 0 {
		t.Fatalf("HTTP hits = %d, want 0 for disabled quota", hits)
	}
	if q.Available {
		t.Fatalf("disabled quota must not be available: %+v", q)
	}
	if q.Source != QuotaSourceDisabled {
		t.Fatalf("source = %q, want %q", q.Source, QuotaSourceDisabled)
	}
	if q.ProviderKeyID != ep.ID {
		t.Fatalf("provider_key_id = %d", q.ProviderKeyID)
	}
}

func TestQuotaRefreshAll_ReportsDisabledAndNotConfiguredSeparately(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":{"usage":1,"limit":10},"account":{}}`))
	}))
	defer okSrv.Close()

	keyRepo, st, mk := openQuotaRefreshStore(t)

	active, err := keyRepo.AddEndpointWithQuota(ProviderTavily, "active", okSrv.URL, "tvly-active-key-111111", EndpointQuotaInput{
		Mode: QuotaEndpointCredentials, Flow: QuotaFlowTavilyUsage,
	})
	if err != nil {
		t.Fatal(err)
	}

	disabled, err := keyRepo.AddEndpointWithQuota(ProviderTavily, "disabled-q", "https://api.tavily.com", "tvly-disabled-key-2222", EndpointQuotaInput{
		Mode: QuotaDisabled, Flow: QuotaFlowTavilyUsage,
	})
	if err != nil {
		t.Fatal(err)
	}

	notCfg, err := keyRepo.AddEndpointWithQuota(ProviderTavily, "not-cfg", "https://api.tavily.com", "tvly-notcfg-key-333333", EndpointQuotaInput{
		Mode: QuotaEndpointCredentials, Flow: QuotaFlowTavilyUsage,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Switch to separate mode with URL only (no rotate) → not configured.
	if err := keyRepo.UpdateEndpointQuota(notCfg.ID, EndpointQuotaInput{
		Mode: QuotaSeparateCredentials, Flow: QuotaFlowTavilyUsage, BaseURL: "https://quota.tavily.example",
	}); err != nil {
		t.Fatalf("UpdateEndpointQuota: %v", err)
	}

	ref := &QuotaRefresher{
		HTTPClient:   okSrv.Client(),
		ProviderKeys: keyRepo,
		Settings:     NewSettingsRepo(st.DB()),
		KeyQuotas:    NewKeyQuotaRepo(st.DB()),
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	result, err := ref.RefreshAllKeys(context.Background(), ProviderTavily)
	if err != nil {
		t.Fatalf("RefreshAllKeys: %v", err)
	}
	if result.Attempted != 1 || result.Succeeded != 1 || result.Failed != 0 {
		t.Fatalf("attempted/succeeded/failed = %d/%d/%d, want 1/1/0; full=%#v", result.Attempted, result.Succeeded, result.Failed, result)
	}
	if result.SkippedDisabled != 1 {
		t.Fatalf("SkippedDisabled = %d, want 1", result.SkippedDisabled)
	}
	if result.SkippedNotConfigured != 1 {
		t.Fatalf("SkippedNotConfigured = %d, want 1", result.SkippedNotConfigured)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("HTTP hits = %d, want 1 (only active row)", hits)
	}

	byID := map[int64]KeyQuotaRefreshResult{}
	for _, kr := range result.KeyResults {
		byID[kr.ProviderKeyID] = kr
	}
	if !byID[active.ID].Attempted || byID[active.ID].Quota == nil || !byID[active.ID].Quota.Available {
		t.Fatalf("active result = %#v", byID[active.ID])
	}
	if byID[disabled.ID].Attempted || byID[disabled.ID].SkippedReason == nil || *byID[disabled.ID].SkippedReason != "quota_disabled" {
		t.Fatalf("disabled result = %#v", byID[disabled.ID])
	}
	if byID[notCfg.ID].Attempted || byID[notCfg.ID].SkippedReason == nil || *byID[notCfg.ID].SkippedReason != "quota_not_configured" {
		t.Fatalf("not_configured result = %#v", byID[notCfg.ID])
	}
}

func TestQuotaRefreshFailure_DoesNotChangeInferenceCooldownOrOrder(t *testing.T) {
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer failSrv.Close()

	keyRepo, st, mk := openQuotaRefreshStore(t)
	ep, err := keyRepo.AddEndpointWithQuota(ProviderTavily, "iso", failSrv.URL, "tvly-iso-fail-key-11111", EndpointQuotaInput{
		Mode: QuotaEndpointCredentials, Flow: QuotaFlowTavilyUsage,
	})
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().UTC().Add(2 * time.Hour)
	reason := "rate_limited"
	if err := keyRepo.MarkFailureWithCooldown(ep.ID, 429, "limited", &until, &reason); err != nil {
		t.Fatal(err)
	}

	type snap struct {
		BaseURL, EncKey, CooldownUntil, CooldownReason, LastFailedAt, ArchivedAt string
		Enabled                                                                  int
	}
	read := func() snap {
		t.Helper()
		var s snap
		var enc []byte
		if err := st.DB().QueryRow(`
			SELECT base_url, encrypted_key,
				COALESCE(cooldown_until,''), COALESCE(cooldown_reason,''), COALESCE(last_failed_at,''),
				enabled, COALESCE(archived_at,'')
			FROM provider_keys WHERE id = ?`, ep.ID).Scan(
			&s.BaseURL, &enc, &s.CooldownUntil, &s.CooldownReason, &s.LastFailedAt, &s.Enabled, &s.ArchivedAt,
		); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		s.EncKey = string(enc)
		return s
	}
	before := read()
	if before.CooldownUntil == "" || before.LastFailedAt == "" {
		t.Fatalf("expected inference routing state set: %+v", before)
	}

	ref := &QuotaRefresher{
		HTTPClient:   failSrv.Client(),
		ProviderKeys: keyRepo,
		Settings:     NewSettingsRepo(st.DB()),
		KeyQuotas:    NewKeyQuotaRepo(st.DB()),
		MasterKey:    mk,
		Now:          func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) },
	}
	q, err := ref.RefreshKey(context.Background(), ep.ID)
	if err != nil {
		t.Fatalf("RefreshKey: %v", err)
	}
	if q.Available {
		t.Fatalf("expected quota failure, got available: %+v", q)
	}

	after := read()
	if after.BaseURL != before.BaseURL || after.EncKey != before.EncKey {
		t.Fatal("inference pair changed by quota refresh failure")
	}
	if after.CooldownUntil != before.CooldownUntil || after.CooldownReason != before.CooldownReason {
		t.Fatal("cooldown changed by quota refresh failure")
	}
	if after.LastFailedAt != before.LastFailedAt {
		t.Fatal("last_failed_at (order) changed by quota refresh failure")
	}
	if after.Enabled != before.Enabled || after.ArchivedAt != before.ArchivedAt {
		t.Fatal("enabled/archived changed by quota refresh failure")
	}
}

func TestInferenceFailure_DoesNotChangeQuotaConfiguration(t *testing.T) {
	keyRepo, st, _ := openQuotaRefreshStore(t)
	ep, err := keyRepo.AddEndpointWithQuota(ProviderGrok, "inf-fail", "https://new-api.example/v1", "xai-inf-fail-key-111111", EndpointQuotaInput{
		Mode: QuotaSeparateCredentials, Flow: QuotaFlowGrok2APIAdmin,
		BaseURL: "https://grok2api.example", RawKey: "g2a-quota-key-stay-put-aaaa",
	})
	if err != nil {
		t.Fatal(err)
	}

	type qsnap struct {
		Mode, Flow, BaseURL, Prefix, Fingerprint string
		EncLen                                   int
	}
	readQ := func() qsnap {
		t.Helper()
		var s qsnap
		var base, prefix, fp *string
		var enc []byte
		if err := st.DB().QueryRow(`
			SELECT quota_mode, quota_flow, quota_base_url, encrypted_quota_key,
				quota_key_prefix, quota_key_fingerprint
			FROM provider_keys WHERE id = ?`, ep.ID).Scan(
			&s.Mode, &s.Flow, &base, &enc, &prefix, &fp,
		); err != nil {
			t.Fatalf("read quota: %v", err)
		}
		if base != nil {
			s.BaseURL = *base
		}
		s.EncLen = len(enc)
		if prefix != nil {
			s.Prefix = *prefix
		}
		if fp != nil {
			s.Fingerprint = *fp
		}
		return s
	}
	before := readQ()
	if before.Mode != string(QuotaSeparateCredentials) || before.BaseURL == "" || before.EncLen == 0 {
		t.Fatalf("expected separate quota configured: %+v", before)
	}

	until := time.Now().UTC().Add(time.Hour)
	reason := "upstream_5xx"
	if err := keyRepo.MarkFailureWithCooldown(ep.ID, 503, "upstream down", &until, &reason); err != nil {
		t.Fatalf("MarkFailureWithCooldown: %v", err)
	}
	if err := keyRepo.MarkFailure(ep.ID, 500, "again"); err != nil {
		t.Fatalf("MarkFailure: %v", err)
	}

	after := readQ()
	if after != before {
		t.Fatalf("quota config changed by inference failure:\nbefore=%+v\nafter=%+v", before, after)
	}
	got, err := keyRepo.Get(ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.QuotaMode != QuotaSeparateCredentials || !got.QuotaKeyConfigured {
		t.Fatalf("display quota metadata corrupted: %+v", got)
	}
}
