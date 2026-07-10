package providers_test

import (
	"testing"

	"code-guda-gateway/internal/providers"
)

func TestNormalizeBaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "https root", raw: "https://api.tavily.com/", want: "https://api.tavily.com"},
		{name: "http self hosted", raw: "http://127.0.0.1:8080/v1/", want: "http://127.0.0.1:8080/v1"},
		{name: "preserve proxy prefix", raw: "https://proxy.example/gateway/firecrawl/v2", want: "https://proxy.example/gateway/firecrawl/v2"},
		{name: "userinfo", raw: "https://user:pass@example.com/v1", wantErr: true},
		{name: "query", raw: "https://example.com/v1?api_key=secret", wantErr: true},
		{name: "fragment", raw: "https://example.com/v1#part", wantErr: true},
		{name: "unsupported scheme", raw: "ftp://example.com/v1", wantErr: true},
		{name: "missing host", raw: "/v1", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := providers.NormalizeBaseURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeBaseURL(%q) expected error, got %q", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeBaseURL(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestJoinEndpointURL_PreservesConfiguredPrefix(t *testing.T) {
	t.Parallel()
	got, err := providers.JoinEndpointURL("https://proxy.example/root/v2", "/scrape", "limit=10")
	if err != nil {
		t.Fatalf("JoinEndpointURL: %v", err)
	}
	if got != "https://proxy.example/root/v2/scrape?limit=10" {
		t.Fatalf("joined URL = %q", got)
	}
}
