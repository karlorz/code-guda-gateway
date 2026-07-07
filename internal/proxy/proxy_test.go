package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-guda-gateway/internal/keypool"
)

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

	req := httptest.NewRequest(http.MethodPost, "/tavily/search?debug=1", strings.NewReader(`{"query":"go"}`))
	req.Header.Set("Authorization", "Bearer inbound")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler := New(Options{
		Client: http.DefaultClient,
		RetryStatuses: map[int]bool{
			http.StatusTooManyRequests: true,
		},
	})
	result := handler.Forward(rec, req, Target{
		BaseURL: upstream.URL,
		Path:    "/search",
		Keys:    keypool.New([]string{"upstream-key"}),
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

func TestForwardRetriesNextKeyOnRetryableStatus(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodPost, "/firecrawl/scrape", strings.NewReader(`{"url":"https://example.com"}`))
	rec := httptest.NewRecorder()
	handler := New(Options{Client: http.DefaultClient, RetryStatuses: DefaultRetryStatuses()})

	result := handler.Forward(rec, req, Target{
		BaseURL: upstream.URL,
		Path:    "/scrape",
		Keys:    keypool.New([]string{"first", "second"}),
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
}
