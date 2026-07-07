package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/store"
)

func openTestApp(t *testing.T, cfg config.Config) (http.Handler, *gatewaykeys.Service, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	gk := gatewaykeys.NewService(st.DB())
	raw, _, err := gk.Create("test-gateway-key")
	if err != nil {
		t.Fatalf("Create gateway key: %v", err)
	}
	return New(cfg, gk), gk, raw
}

func TestHealthDoesNotRequireAuth(t *testing.T) {
	app, _, _ := openTestApp(t, config.Config{GatewayKeys: []string{"unused"}})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestServer_RuntimeRouteRequiresGatewayKey(t *testing.T) {
	app, _, _ := openTestApp(t, config.Config{GatewayKeys: []string{"unused"}})
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

func TestServer_RuntimeRouteAcceptsValidGatewayKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	app, _, raw := openTestApp(t, config.Config{
		GatewayKeys: []string{"unused"},
		GrokBaseURL: upstream.URL + "/grok/v1",
		GrokKeys:    []string{"grok-key"},
	})

	req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestServer_RuntimeRouteRejectsRevokedGatewayKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	app, gk, raw := openTestApp(t, config.Config{
		GatewayKeys: []string{"unused"},
		GrokBaseURL: upstream.URL + "/grok/v1",
		GrokKeys:    []string{"grok-key"},
	})
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

	gk := gatewaykeys.NewService(st.DB())
	raw, _, err := gk.Create("test-gateway-key")
	if err != nil {
		t.Fatalf("Create gateway key: %v", err)
	}
	app := New(config.Config{GatewayKeys: []string{"unused"}}, gk)

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

	app, _, raw := openTestApp(t, config.Config{
		GatewayKeys:      []string{"unused"},
		GrokBaseURL:      upstream.URL + "/grok/v1",
		GrokKeys:         []string{"grok-key"},
		TavilyBaseURL:    upstream.URL + "/tavily",
		TavilyKeys:       []string{"tavily-key"},
		FirecrawlBaseURL: upstream.URL + "/firecrawl",
		FirecrawlKeys:    []string{"firecrawl-key"},
	})

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