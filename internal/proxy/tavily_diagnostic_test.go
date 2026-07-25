package proxy

import (
	"net/http"
	"strings"
	"testing"

	"code-guda-gateway/internal/providers"
)

func TestTavilyAttemptDiagnostic(t *testing.T) {
	paddedBody := func(size int) string {
		const prefix = `{"detail":{"error":"Query is too long. Max query length is 400 characters."},"padding":"`
		const suffix = `"}`
		return prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
	}
	tests := []struct {
		name     string
		provider string
		status   int
		body     string
		wantOK   bool
	}{
		{
			name:     "recognized nested error",
			provider: providers.ProviderTavily,
			status:   http.StatusBadRequest,
			body:     `{"detail":{"error":"Query is too long. Max query length is 400 characters."}}`,
			wantOK:   true,
		},
		{
			name:     "recognized case variation",
			provider: providers.ProviderTavily,
			status:   http.StatusBadRequest,
			body:     `{"detail":{"error":"QUERY exceeds the MAX QUERY LENGTH"}}`,
			wantOK:   true,
		},
		{name: "other provider", provider: providers.ProviderFirecrawl, status: http.StatusBadRequest, body: `{"detail":{"error":"Query is too long"}}`},
		{name: "other status", provider: providers.ProviderTavily, status: http.StatusUnprocessableEntity, body: `{"detail":{"error":"Query is too long"}}`},
		{name: "malformed JSON", provider: providers.ProviderTavily, status: http.StatusBadRequest, body: `{"detail":`},
		{name: "HTML", provider: providers.ProviderTavily, status: http.StatusBadRequest, body: `<html>Query is too long</html>`},
		{name: "unrecognized validation", provider: providers.ProviderTavily, status: http.StatusBadRequest, body: `{"detail":{"error":"Invalid include_domains value"}}`},
		{name: "negated phrase", provider: providers.ProviderTavily, status: http.StatusBadRequest, body: `{"detail":{"error":"Query is not too long"}}`},
		{name: "invalid setting phrase", provider: providers.ProviderTavily, status: http.StatusBadRequest, body: `{"detail":{"error":"Invalid max query length setting"}}`},
		{name: "different documented maximum", provider: providers.ProviderTavily, status: http.StatusBadRequest, body: `{"detail":{"error":"Query is too long. Max query length is 300 characters."}}`},
		{name: "wrong shape", provider: providers.ProviderTavily, status: http.StatusBadRequest, body: `{"error":"Query is too long"}`},
		{name: "body at diagnostic limit", provider: providers.ProviderTavily, status: http.StatusBadRequest, body: paddedBody(maxTavilyDiagnosticBodyBytes), wantOK: true},
		{name: "body over diagnostic limit", provider: providers.ProviderTavily, status: http.StatusBadRequest, body: paddedBody(maxTavilyDiagnosticBodyBytes + 1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason, message, ok := tavilyAttemptDiagnostic(tc.provider, tc.status, []byte(tc.body))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (reason=%q message=%q)", ok, tc.wantOK, reason, message)
			}
			if !ok {
				if reason != "" || message != "" {
					t.Fatalf("unrecognized body returned reason=%q message=%q", reason, message)
				}
				return
			}
			if reason != tavilyQueryTooLongReason {
				t.Fatalf("reason = %q, want %q", reason, tavilyQueryTooLongReason)
			}
			if message != tavilyQueryTooLongMessage {
				t.Fatalf("message = %q, want %q", message, tavilyQueryTooLongMessage)
			}
		})
	}
}
