package providers_test

import (
	"errors"
	"path/filepath"
	"testing"

	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/store"
)

func openTestDB(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestGrokBaseURL_Default(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	url, err := svc.GetBaseURL(providers.ProviderGrok)
	if err != nil {
		t.Fatalf("GetBaseURL: %v", err)
	}
	if url != providers.DefaultGrokBaseURL {
		t.Fatalf("grok default = %q, want %q", url, providers.DefaultGrokBaseURL)
	}
}

func TestGrokBaseURL_SetAndGet(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	custom := "https://new.karldigi.dev/v1"
	if err := svc.SetBaseURL(providers.ProviderGrok, custom); err != nil {
		t.Fatalf("SetBaseURL: %v", err)
	}
	url, err := svc.GetBaseURL(providers.ProviderGrok)
	if err != nil {
		t.Fatalf("GetBaseURL: %v", err)
	}
	if url != custom {
		t.Fatalf("got %q, want %q", url, custom)
	}
}

func TestTavilyFirecrawl_Defaults(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	tavily, err := svc.GetBaseURL(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("tavily GetBaseURL: %v", err)
	}
	if tavily != providers.DefaultTavilyBaseURL {
		t.Fatalf("tavily = %q, want %q", tavily, providers.DefaultTavilyBaseURL)
	}

	fc, err := svc.GetBaseURL(providers.ProviderFirecrawl)
	if err != nil {
		t.Fatalf("firecrawl GetBaseURL: %v", err)
	}
	if fc != providers.DefaultFirecrawlBaseURL {
		t.Fatalf("firecrawl = %q, want %q", fc, providers.DefaultFirecrawlBaseURL)
	}
}

func TestGetBaseURL_RejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	_, err := svc.GetBaseURL("bogus")
	if !errors.Is(err, providers.ErrUnknownProvider) {
		t.Fatalf("GetBaseURL err = %v, want ErrUnknownProvider", err)
	}
}

func TestSetBaseURL_RejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	err := svc.SetBaseURL("bogus", "https://example.com")
	if !errors.Is(err, providers.ErrUnknownProvider) {
		t.Fatalf("SetBaseURL err = %v, want ErrUnknownProvider", err)
	}
}
