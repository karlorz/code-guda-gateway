package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-guda-gateway/internal/config"
)

func TestHealthDoesNotRequireAuth(t *testing.T) {
	app := New(config.Config{GatewayKeys: []string{"dev"}})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestProtectedRoutesRequireBearerAuth(t *testing.T) {
	app := New(config.Config{GatewayKeys: []string{"dev"}})
	req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRoutesForwardToExpectedUpstreams(t *testing.T) {
	var seen []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	app := New(config.Config{
		GatewayKeys:      []string{"dev"},
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
		req.Header.Set("Authorization", "Bearer dev")
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
