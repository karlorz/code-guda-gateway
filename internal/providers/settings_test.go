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
		t.Fatalf("grok base = %q, want %q", url, custom)
	}
}

func TestTavilyBaseURL_Default(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	url, err := svc.GetBaseURL(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("GetBaseURL: %v", err)
	}
	if url != providers.DefaultTavilyBaseURL {
		t.Fatalf("tavily default = %q, want %q", url, providers.DefaultTavilyBaseURL)
	}
}

func TestFirecrawlBaseURL_Default(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	url, err := svc.GetBaseURL(providers.ProviderFirecrawl)
	if err != nil {
		t.Fatalf("GetBaseURL: %v", err)
	}
	if url != providers.DefaultFirecrawlBaseURL {
		t.Fatalf("firecrawl default = %q, want %q", url, providers.DefaultFirecrawlBaseURL)
	}
}

func TestBaseURL_UnknownProvider(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	_, err := svc.GetBaseURL("not-a-provider")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !errors.Is(err, providers.ErrUnknownProvider) && err.Error() == "" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetBaseURL_RejectsUnsafeURL(t *testing.T) {
	t.Parallel()
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	if err := svc.SetBaseURL(providers.ProviderGrok, "https://user:pass@evil.example/v1"); err == nil {
		t.Fatal("expected error for userinfo URL")
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
	if got.Timezone == "Local" {
		t.Fatal(`host default timezone must not be bare "Local" (breaks JS Intl)`)
	}
	if _, err := time.LoadLocation(got.Timezone); err != nil {
		t.Fatalf("LoadLocation(%q): %v", got.Timezone, err)
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
	if got.Timezone == "Local" {
		t.Fatal(`host default after clear must not be "Local"`)
	}
	if _, err := time.LoadLocation(got.Timezone); err != nil {
		t.Fatalf("LoadLocation(%q) after clear: %v", got.Timezone, err)
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

func TestDisplayTimezone_HostFromTZEnv(t *testing.T) {
	// Not parallel: mutates process TZ via t.Setenv.
	t.Setenv("TZ", "Pacific/Auckland")
	st := openTestDB(t)
	svc := providers.NewSettingsRepo(st.DB())

	got, err := svc.GetDisplayTimezone()
	if err != nil {
		t.Fatalf("GetDisplayTimezone: %v", err)
	}
	if got.Source != "host" {
		t.Fatalf("source = %q, want host", got.Source)
	}
	if got.Timezone != "Pacific/Auckland" {
		t.Fatalf("timezone = %q, want Pacific/Auckland from TZ", got.Timezone)
	}
}
