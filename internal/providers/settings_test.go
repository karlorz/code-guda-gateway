package providers_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

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

func TestSetBaseURL_RejectsInvalidURLs(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())
	invalids := []string{
		"https://user:pass@example.com/v1",
		"https://example.com/v1?api_key=secret",
		"https://example.com/v1#frag",
		"ftp://example.com/v1",
		"/relative",
	}
	for _, raw := range invalids {
		err := svc.SetBaseURL(providers.ProviderGrok, raw)
		if err == nil {
			t.Fatalf("SetBaseURL(%q) expected error", raw)
		}
		if !errors.Is(err, providers.ErrInvalidBaseURL) {
			t.Fatalf("SetBaseURL(%q) err = %v, want ErrInvalidBaseURL", raw, err)
		}
	}
	// Valid URL still works and is normalized (trailing slash stripped).
	if err := svc.SetBaseURL(providers.ProviderGrok, "https://custom.example/v1/"); err != nil {
		t.Fatalf("valid SetBaseURL: %v", err)
	}
	got, err := svc.GetBaseURL(providers.ProviderGrok)
	if err != nil {
		t.Fatalf("GetBaseURL: %v", err)
	}
	if got != "https://custom.example/v1" {
		t.Fatalf("got %q, want normalized URL", got)
	}
}

func TestDisplayTimezone_DefaultIsHost(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	got, err := svc.GetDisplayTimezone()
	if err != nil {
		t.Fatalf("GetDisplayTimezone: %v", err)
	}
	if got.Source != "host" {
		t.Fatalf("source = %q, want host", got.Source)
	}
	if got.Timezone == "" {
		t.Fatal("timezone empty")
	}
	// Effective zone must load.
	if _, err := time.LoadLocation(got.Timezone); err != nil {
		// time.Local.String() can be "Local" on some systems — accept if LoadLocation fails but equal to time.Local.String()
		if got.Timezone != time.Local.String() {
			t.Fatalf("LoadLocation(%q): %v", got.Timezone, err)
		}
	}
}

func TestDisplayTimezone_SetGetClear(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	if err := svc.SetDisplayTimezone("Asia/Seoul"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := svc.GetDisplayTimezone()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Timezone != "Asia/Seoul" || got.Source != "stored" {
		t.Fatalf("got %#v", got)
	}

	if err := svc.SetDisplayTimezone(""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err = svc.GetDisplayTimezone()
	if err != nil {
		t.Fatalf("Get after clear: %v", err)
	}
	if got.Source != "host" {
		t.Fatalf("source after clear = %q", got.Source)
	}
}

func TestDisplayTimezone_RejectsInvalid(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	if err := svc.SetDisplayTimezone("Not/A_Zone"); err == nil {
		t.Fatal("expected error for invalid zone")
	}
	got, err := svc.GetDisplayTimezone()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Source != "host" {
		t.Fatalf("invalid set mutated storage: %#v", got)
	}
}
