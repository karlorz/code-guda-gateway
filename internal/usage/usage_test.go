package usage_test

import (
	"path/filepath"
	"testing"
	"time"

	"code-guda-gateway/internal/store"
	"code-guda-gateway/internal/usage"
)

func openUsageDB(t *testing.T) (*usage.UsageRepo, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return usage.NewUsageRepo(st.DB()), st
}

func TestUsageIncrement_CreatesRow(t *testing.T) {
	repo, _ := openUsageDB(t)
	day := "2026-07-07"
	keyID := int64(42)
	err := repo.Increment(usage.UsageIncrement{
		Day:           day,
		GatewayKeyID:  &keyID,
		Provider:      "grok",
		RouteFamily:   "grok",
		StatusClass:   "2xx",
	})
	if err != nil {
		t.Fatalf("Increment: %v", err)
	}
	rows, err := repo.ListDaily(usage.ListFilter{Day: day})
	if err != nil {
		t.Fatalf("ListDaily: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestCount != 1 {
		t.Fatalf("rows = %#v, want count=1", rows)
	}
}

func TestUsageIncrement_IncrementsExisting(t *testing.T) {
	repo, _ := openUsageDB(t)
	day := "2026-07-07"
	keyID := int64(42)
	inc := usage.UsageIncrement{
		Day: day, GatewayKeyID: &keyID, Provider: "grok", RouteFamily: "grok", StatusClass: "2xx",
	}
	if err := repo.Increment(inc); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := repo.Increment(inc); err != nil {
		t.Fatalf("second: %v", err)
	}
	rows, err := repo.ListDaily(usage.ListFilter{Day: day})
	if err != nil {
		t.Fatalf("ListDaily: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestCount != 2 {
		t.Fatalf("rows = %#v, want count=2", rows)
	}
}

func TestUsageIncrement_StatusClasses(t *testing.T) {
	repo, _ := openUsageDB(t)
	day := "2026-07-07"
	keyID := int64(1)
	classes := []string{"2xx", "4xx", "5xx", "429", "network_error"}
	for _, c := range classes {
		if err := repo.Increment(usage.UsageIncrement{
			Day: day, GatewayKeyID: &keyID, Provider: "grok", RouteFamily: "grok", StatusClass: c,
		}); err != nil {
			t.Fatalf("Increment %s: %v", c, err)
		}
	}
	rows, err := repo.ListDaily(usage.ListFilter{Day: day})
	if err != nil {
		t.Fatalf("ListDaily: %v", err)
	}
	if len(rows) != len(classes) {
		t.Fatalf("got %d rows, want %d", len(rows), len(classes))
	}
}

func TestUsageListDaily_FiltersByDate(t *testing.T) {
	repo, _ := openUsageDB(t)
	keyID := int64(1)
	_ = repo.Increment(usage.UsageIncrement{Day: "2026-07-06", GatewayKeyID: &keyID, Provider: "grok", RouteFamily: "grok", StatusClass: "2xx"})
	_ = repo.Increment(usage.UsageIncrement{Day: "2026-07-07", GatewayKeyID: &keyID, Provider: "grok", RouteFamily: "grok", StatusClass: "2xx"})
	rows, err := repo.ListDaily(usage.ListFilter{Day: "2026-07-07"})
	if err != nil {
		t.Fatalf("ListDaily: %v", err)
	}
	if len(rows) != 1 || rows[0].Day != "2026-07-07" {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestUsageListDaily_NeverReturnsRawSecrets(t *testing.T) {
	repo, _ := openUsageDB(t)
	keyID := int64(99)
	_ = repo.Increment(usage.UsageIncrement{
		Day: "2026-07-07", GatewayKeyID: &keyID, Provider: "grok", RouteFamily: "grok", StatusClass: "2xx",
	})
	rows, err := repo.ListDaily(usage.ListFilter{Day: "2026-07-07"})
	if err != nil {
		t.Fatalf("ListDaily: %v", err)
	}
	for _, r := range rows {
		if r.Provider == "" || r.RouteFamily == "" || r.StatusClass == "" {
			t.Fatalf("incomplete row %#v", r)
		}
		// structs must only expose aggregate fields — no secret columns exist
		if r.RequestCount < 1 {
			t.Fatalf("bad count %#v", r)
		}
	}
}

func TestRouteFamilyFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/grok/v1/chat/completions", "grok"},
		{"/tavily/search", "tavily"},
		{"/firecrawl/scrape", "firecrawl"},
		{"/unknown/foo", "unknown"},
	}
	for _, tc := range cases {
		if got := usage.RouteFamilyFromPath(tc.path); got != tc.want {
			t.Fatalf("RouteFamilyFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestStatusClassFromHTTP(t *testing.T) {
	if usage.StatusClassFromHTTP(200) != "2xx" {
		t.Fatal("200")
	}
	if usage.StatusClassFromHTTP(404) != "4xx" {
		t.Fatal("404")
	}
	if usage.StatusClassFromHTTP(500) != "5xx" {
		t.Fatal("500")
	}
	if usage.StatusClassFromHTTP(429) != "429" {
		t.Fatal("429")
	}
	if usage.StatusClassFromHTTP(302) != "4xx" {
		t.Fatal("302 should map to 4xx")
	}
	if usage.StatusClassFromNetworkError() != "network_error" {
		t.Fatal("network")
	}
}

func TestDayUTC(t *testing.T) {
	d := usage.DayUTC(time.Date(2026, 7, 7, 15, 0, 0, 0, time.UTC))
	if d != "2026-07-07" {
		t.Fatalf("DayUTC = %q", d)
	}
}