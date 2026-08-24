package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/store"
	"code-guda-gateway/internal/usage"
)

func openTestApp(t *testing.T, cfg config.Config) (http.Handler, *gatewaykeys.Service, *providers.KeyRepo, *store.Store, string) {
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
	gk := gatewaykeys.NewService(st.DB())
	raw, _, err := gk.Create("test-gateway-key")
	if err != nil {
		t.Fatalf("Create gateway key: %v", err)
	}
	keyRepo := providers.NewKeyRepo(st.DB(), mk)
	return New(cfg, gk, st.DB(), mk), gk, keyRepo, st, raw
}

func TestInternalKeysVerify(t *testing.T) {
	const secretToken = "internal-secret-xyz"

	t.Run("POST verify without X-Internal-Token -> 403", func(t *testing.T) {
		app, _, _, _, raw := openTestApp(t, config.Config{InternalToken: secretToken})
		body := `{"token":"` + raw + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		if strings.Contains(rec.Body.String(), raw) {
			t.Fatal("response body leaked raw gateway key")
		}
	})

	t.Run("POST verify with wrong X-Internal-Token -> 403", func(t *testing.T) {
		app, _, _, _, raw := openTestApp(t, config.Config{InternalToken: secretToken})
		body := `{"token":"` + raw + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", "wrong-secret")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		if strings.Contains(rec.Body.String(), raw) {
			t.Fatal("response body leaked raw gateway key")
		}
	})

	t.Run("POST verify with correct internal token + unknown gsk_ token -> 401", func(t *testing.T) {
		app, _, _, _, _ := openTestApp(t, config.Config{InternalToken: secretToken})
		unknownKey := "gsk_01234567890123456789012345678901"
		body := `{"token":"` + unknownKey + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", secretToken)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if strings.Contains(rec.Body.String(), unknownKey) {
			t.Fatal("response body leaked raw token")
		}
	})

	t.Run("POST verify with correct internal token + disabled/revoked key -> 401", func(t *testing.T) {
		app, gk, _, _, raw := openTestApp(t, config.Config{InternalToken: secretToken})
		list, err := gk.List()
		if err != nil || len(list) == 0 {
			t.Fatalf("list gateway keys: %v", err)
		}
		if err := gk.Disable(list[0].ID); err != nil {
			t.Fatalf("disable gateway key: %v", err)
		}

		body := `{"token":"` + raw + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", secretToken)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("POST verify with correct internal token + enabled key -> 200", func(t *testing.T) {
		app, gk, _, _, raw := openTestApp(t, config.Config{InternalToken: secretToken})
		// Create a key with a name and another without a name to test client_id fallback
		rawUnnamed, dispUnnamed, err := gk.Create("")
		if err != nil {
			t.Fatalf("create unnamed key: %v", err)
		}

		// 1. Test named key
		body := `{"token":"` + raw + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", secretToken)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal json: %v", err)
		}

		if resp["name"] != "test-gateway-key" {
			t.Fatalf("name = %v, want test-gateway-key", resp["name"])
		}
		if resp["client_id"] != "test-gateway-key" {
			t.Fatalf("client_id = %v, want test-gateway-key", resp["client_id"])
		}
		if resp["prefix"] == nil || resp["prefix"] == "" {
			t.Fatalf("prefix missing or empty: %v", resp["prefix"])
		}
		if resp["fingerprint"] == nil || resp["fingerprint"] == "" {
			t.Fatalf("fingerprint missing or empty: %v", resp["fingerprint"])
		}
		scopes, ok := resp["scopes"].([]interface{})
		if !ok || len(scopes) != 1 || scopes[0] != "mcp" {
			t.Fatalf("scopes = %v, want [\"mcp\"]", resp["scopes"])
		}
		if _, exists := resp["key_hash"]; exists {
			t.Fatal("response must never include key_hash")
		}
		if _, exists := resp["key"]; exists || strings.Contains(rec.Body.String(), raw) {
			t.Fatal("response must never include raw key")
		}

		// 2. Test unnamed key client_id fallback to prefix
		bodyUnnamed := `{"token":"` + rawUnnamed + `"}`
		req2 := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(bodyUnnamed))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("X-Internal-Token", secretToken)
		rec2 := httptest.NewRecorder()

		app.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusOK {
			t.Fatalf("unnamed key status = %d, want 200", rec2.Code)
		}
		var resp2 map[string]interface{}
		if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
			t.Fatalf("unmarshal json: %v", err)
		}
		if resp2["client_id"] != dispUnnamed.Prefix {
			t.Fatalf("unnamed client_id = %v, want %v", resp2["client_id"], dispUnnamed.Prefix)
		}
	})

	t.Run("GET /internal/keys/verify -> 405", func(t *testing.T) {
		app, _, _, _, _ := openTestApp(t, config.Config{InternalToken: secretToken})
		req := httptest.NewRequest(http.MethodGet, "/internal/keys/verify", nil)
		req.Header.Set("X-Internal-Token", secretToken)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
	})

	t.Run("GET /internal/keys/verify without internal token -> 405 or 403", func(t *testing.T) {
		app, _, _, _, _ := openTestApp(t, config.Config{InternalToken: secretToken})
		req := httptest.NewRequest(http.MethodGet, "/internal/keys/verify", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 405 or 403 (not 401, not 200)", rec.Code)
		}
	})

	t.Run("Empty GUDA_INTERNAL_TOKEN in config + any POST verify -> 403; healthz 200", func(t *testing.T) {
		app, _, _, _, raw := openTestApp(t, config.Config{InternalToken: ""})
		body := `{"token":"` + raw + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", "some-token")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}

		hreq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		hrec := httptest.NewRecorder()
		app.ServeHTTP(hrec, hreq)
		if hrec.Code != http.StatusOK {
			t.Fatalf("healthz status = %d, want 200", hrec.Code)
		}
	})

	t.Run("Malformed JSON / missing token field -> 400", func(t *testing.T) {
		app, _, _, _, _ := openTestApp(t, config.Config{InternalToken: secretToken})

		// Bad JSON
		req1 := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(`{invalid-json`))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("X-Internal-Token", secretToken)
		rec1 := httptest.NewRecorder()
		app.ServeHTTP(rec1, req1)
		if rec1.Code != http.StatusBadRequest {
			t.Fatalf("bad json status = %d, want 400", rec1.Code)
		}

		// Missing token field
		req2 := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(`{"other":"field"}`))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("X-Internal-Token", secretToken)
		rec2 := httptest.NewRecorder()
		app.ServeHTTP(rec2, req2)
		if rec2.Code != http.StatusBadRequest {
			t.Fatalf("missing token status = %d, want 400", rec2.Code)
		}
	})
}

func TestHealthDoesNotRequireAuth(t *testing.T) {
	app, _, _, _, _ := openTestApp(t, config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestUnknownPathWithoutAuthIsNotFound(t *testing.T) {
	app, _, _, _, _ := openTestApp(t, config.Config{})
	for _, path := range []string{"/favicon.ico", "/v1/models", "/robots.txt"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 (not 401 — unknown paths must not require gateway auth)", path, rec.Code)
		}
	}
}

func TestServer_RuntimeRouteRequiresGatewayKey(t *testing.T) {
	app, _, _, _, _ := openTestApp(t, config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestProtectedRoutesRequireBearerAuth(t *testing.T) {
	TestServer_RuntimeRouteRequiresGatewayKey(t)
}

func TestServer_RuntimeRequestIncrementsUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	app, gk, keyRepo, st, raw := openTestApp(t, config.Config{})
	if _, err := keyRepo.AddEndpoint(providers.ProviderGrok, "primary", upstream.URL+"/grok/v1", "grok-key"); err != nil {
		t.Fatalf("AddEndpoint grok: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	usageRepo := usage.NewUsageRepo(st.DB())
	day := usage.DayUTC(time.Now())
	rows, err := usageRepo.ListDaily(usage.ListFilter{Day: day})
	if err != nil {
		t.Fatalf("ListDaily: %v", err)
	}
	list, _ := gk.List()
	if len(list) == 0 {
		t.Fatal("no gateway keys")
	}
	wantKeyID := list[0].ID
	var found bool
	for _, row := range rows {
		if row.RouteFamily == "grok" && row.StatusClass == "2xx" && row.Provider == providers.ProviderGrok &&
			row.GatewayKeyID != nil && *row.GatewayKeyID == wantKeyID && row.RequestCount >= 1 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("usage rows = %#v, want grok/2xx for key %d", rows, wantKeyID)
	}
}

func TestServer_RuntimeRouteAcceptsValidGatewayKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	app, _, keyRepo, _, raw := openTestApp(t, config.Config{})
	if _, err := keyRepo.AddEndpoint(providers.ProviderGrok, "primary", upstream.URL+"/grok/v1", "grok-key"); err != nil {
		t.Fatalf("AddEndpoint grok: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestServer_ProviderDefaultDoesNotReroute proves that changing provider_settings
// after an endpoint row is created does not change the live upstream for that row.
func TestServer_ProviderDefaultDoesNotReroute(t *testing.T) {
	var hitsOriginal atomic.Int32
	var hitsChanged atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsOriginal.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("from-original"))
	}))
	defer original.Close()
	changed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsChanged.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("from-changed"))
	}))
	defer changed.Close()

	app, _, keyRepo, st, raw := openTestApp(t, config.Config{})
	// Snapshot original base URL into the endpoint row at creation time.
	if _, err := keyRepo.AddEndpoint(providers.ProviderGrok, "primary", original.URL+"/grok/v1", "grok-key"); err != nil {
		t.Fatalf("AddEndpoint: %v", err)
	}
	// Mutate provider-wide default after the row exists — must not re-route live traffic.
	if err := providers.NewSettingsRepo(st.DB()).SetBaseURL(providers.ProviderGrok, changed.URL+"/grok/v1"); err != nil {
		t.Fatalf("SetBaseURL after create: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "from-original" {
		t.Fatalf("body = %q, want from-original (row-owned URL)", rec.Body.String())
	}
	if hitsOriginal.Load() != 1 {
		t.Fatalf("original hits = %d, want 1", hitsOriginal.Load())
	}
	if hitsChanged.Load() != 0 {
		t.Fatalf("changed hits = %d, want 0 (provider_settings must not re-route)", hitsChanged.Load())
	}
}

func TestServer_RuntimeRouteRejectsRevokedGatewayKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	app, gk, keyRepo, _, raw := openTestApp(t, config.Config{})
	_, _ = keyRepo.AddEndpoint(providers.ProviderGrok, "primary", upstream.URL+"/grok/v1", "grok-key")
	list, _ := gk.List()
	if len(list) == 0 {
		t.Fatal("no gateway keys")
	}
	if err := gk.Revoke(list[0].ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestServer_RuntimeRouteReturns500OnDBError(t *testing.T) {
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

	gk := gatewaykeys.NewService(st.DB())
	raw, _, err := gk.Create("test-gateway-key")
	if err != nil {
		t.Fatalf("Create gateway key: %v", err)
	}
	app := New(config.Config{}, gk, st.DB(), mk)

	if err := st.DB().Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "internal error") {
		t.Fatalf("body = %q, want internal error", body)
	}
}

func TestServer_HealthzStillOpen(t *testing.T) {
	TestHealthDoesNotRequireAuth(t)
}

func TestRoutesForwardToExpectedUpstreams(t *testing.T) {
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	app, _, keyRepo, _, raw := openTestApp(t, config.Config{})
	// Rows own their base URL; settings SetBaseURL is no longer used for routing.
	_, _ = keyRepo.AddEndpoint(providers.ProviderGrok, "g1", upstream.URL+"/grok/v1", "grok-key")
	_, _ = keyRepo.AddEndpoint(providers.ProviderTavily, "t1", upstream.URL+"/tavily", "tavily-key")
	_, _ = keyRepo.AddEndpoint(providers.ProviderFirecrawl, "f1", upstream.URL+"/firecrawl", "firecrawl-key")

	cases := []struct {
		method string
		path   string
		body   string
		want   string
	}{
		{http.MethodGet, "/grok/v1/models", "", "GET /grok/v1/models Bearer grok-key"},
		{http.MethodPost, "/grok/v1/chat/completions", `{}`, "POST /grok/v1/chat/completions Bearer grok-key"},
		{http.MethodPost, "/tavily/search", `{}`, "POST /tavily/search Bearer tavily-key"},
		{http.MethodPost, "/tavily/extract", `{}`, "POST /tavily/extract Bearer tavily-key"},
		{http.MethodPost, "/tavily/map", `{}`, "POST /tavily/map Bearer tavily-key"},
		{http.MethodPost, "/firecrawl/search", `{}`, "POST /firecrawl/search Bearer firecrawl-key"},
		{http.MethodPost, "/firecrawl/scrape", `{}`, "POST /firecrawl/scrape Bearer firecrawl-key"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+raw)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, want 200", tc.method, tc.path, rec.Code)
		}
	}

	if len(seen) != len(cases) {
		t.Fatalf("seen = %#v", seen)
	}
	for i, tc := range cases {
		if seen[i] != tc.want {
			t.Fatalf("seen[%d] = %q, want %q", i, seen[i], tc.want)
		}
	}
}
