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
