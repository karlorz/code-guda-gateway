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
