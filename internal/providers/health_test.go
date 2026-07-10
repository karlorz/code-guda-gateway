package providers_test

import (
	"strings"
	"testing"

	"code-guda-gateway/internal/providers"
)

func TestBuildHealthDegradesOnLatestProviderEventError(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	settings := providers.NewSettingsRepo(st.DB())
	key, err := repo.Add(providers.ProviderFirecrawl, "primary", "fc-test-key-material")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	status := 401
	if err := repo.MarkLastEvent(key.ID, providers.LastEvent{
		Source:      "quota_refresh",
		StatusClass: "error",
		HTTPStatus:  &status,
		Message:     "quota refresh failed: upstream status 401",
	}); err != nil {
		t.Fatalf("MarkLastEvent: %v", err)
	}

	items, err := providers.BuildHealth(settings, repo)
	if err != nil {
		t.Fatalf("BuildHealth: %v", err)
	}

	var firecrawl providers.HealthItem
	for _, item := range items {
		if item.Provider == providers.ProviderFirecrawl {
			firecrawl = item
			break
		}
	}
	if firecrawl.Status != providers.HealthDegraded {
		t.Fatalf("firecrawl status = %q, want %q: %+v", firecrawl.Status, providers.HealthDegraded, firecrawl)
	}
	if firecrawl.LastEventStatusClass == nil || *firecrawl.LastEventStatusClass != "error" {
		t.Fatalf("last event status class = %v, want error", firecrawl.LastEventStatusClass)
	}
	if firecrawl.LastEventHTTPStatus == nil || *firecrawl.LastEventHTTPStatus != status {
		t.Fatalf("last event http status = %v, want %d", firecrawl.LastEventHTTPStatus, status)
	}
	if firecrawl.LastEventMessageRedacted == nil || !strings.Contains(*firecrawl.LastEventMessageRedacted, "upstream status 401") {
		t.Fatalf("missing degraded reason message: %+v", firecrawl)
	}
	if len(firecrawl.Reasons) == 0 || !strings.Contains(strings.Join(firecrawl.Reasons, " "), "quota_refresh") {
		t.Fatalf("reasons do not mention latest failing event: %+v", firecrawl.Reasons)
	}
}

func TestBuildHealth_BaseURLFromEndpoints(t *testing.T) {
	t.Parallel()
	repo, st, _ := openKeyRepo(t)
	settings := providers.NewSettingsRepo(st.DB())
	// Settings default is NOT the live routing URL.
	if err := settings.SetBaseURL(providers.ProviderTavily, "https://settings-default.example"); err != nil {
		t.Fatalf("SetBaseURL: %v", err)
	}

	// Single distinct enabled URL → show that URL, not settings default.
	if _, err := repo.AddEndpoint(providers.ProviderTavily, "a", "https://proxy-a.example/tavily", "tvly-a-key-11111111"); err != nil {
		t.Fatalf("AddEndpoint a: %v", err)
	}
	items, err := providers.BuildHealth(settings, repo)
	if err != nil {
		t.Fatalf("BuildHealth: %v", err)
	}
	tv := findHealth(t, items, providers.ProviderTavily)
	if tv.BaseURL != "https://proxy-a.example/tavily" {
		t.Fatalf("single endpoint base_url = %q, want row URL", tv.BaseURL)
	}
	if tv.DistinctBaseURLs != 1 {
		t.Fatalf("distinct_base_urls = %d, want 1", tv.DistinctBaseURLs)
	}

	// Mixed enabled URLs → mixed (N endpoints), not a single settings URL.
	if _, err := repo.AddEndpoint(providers.ProviderTavily, "b", "https://proxy-b.example/tavily", "tvly-b-key-22222222"); err != nil {
		t.Fatalf("AddEndpoint b: %v", err)
	}
	items, err = providers.BuildHealth(settings, repo)
	if err != nil {
		t.Fatalf("BuildHealth2: %v", err)
	}
	tv = findHealth(t, items, providers.ProviderTavily)
	if tv.BaseURL != "mixed (2 endpoints)" {
		t.Fatalf("mixed base_url = %q, want mixed (2 endpoints)", tv.BaseURL)
	}
	if tv.DistinctBaseURLs != 2 {
		t.Fatalf("distinct_base_urls = %d, want 2", tv.DistinctBaseURLs)
	}
	if tv.BaseURL == "https://settings-default.example" {
		t.Fatal("must not present settings creation default as live routing URL")
	}

	// No endpoints → fall back to settings default.
	items, err = providers.BuildHealth(settings, repo)
	if err != nil {
		t.Fatalf("BuildHealth3: %v", err)
	}
	grok := findHealth(t, items, providers.ProviderGrok)
	if grok.BaseURL != providers.DefaultGrokBaseURL && grok.KeyCount != 0 {
		// only assert when empty
	}
	if grok.KeyCount != 0 {
		t.Fatalf("expected no grok keys, got %d", grok.KeyCount)
	}
	if grok.BaseURL != providers.DefaultGrokBaseURL {
		// GetBaseURL returns default when unset
		got, _ := settings.GetBaseURL(providers.ProviderGrok)
		if grok.BaseURL != got {
			t.Fatalf("empty pool base_url = %q, want settings default %q", grok.BaseURL, got)
		}
	}
}

func findHealth(t *testing.T, items []providers.HealthItem, provider string) providers.HealthItem {
	t.Helper()
	for _, item := range items {
		if item.Provider == provider {
			return item
		}
	}
	t.Fatalf("provider %s not in health items", provider)
	return providers.HealthItem{}
}
