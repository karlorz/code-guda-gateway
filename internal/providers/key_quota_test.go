package providers_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/store"
)

func openKeyQuotaDB(t *testing.T) (*providers.KeyRepo, *providers.KeyQuotaRepo, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mk, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	return providers.NewKeyRepo(st.DB(), mk), providers.NewKeyQuotaRepo(st.DB()), st
}

func TestKeyQuotaRepo_UpsertAndPoolPage(t *testing.T) {
	keys, quotas, _ := openKeyQuotaDB(t)
	key, err := keys.Add(providers.ProviderTavily, "tavily-1", "tvly-secret-123456")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	remaining := int64(850)
	limit := int64(1000)
	msg := "ok"
	if err := quotas.Upsert(providers.ProviderKeyQuota{
		ProviderKeyID:   key.ID,
		Provider:        providers.ProviderTavily,
		Source:          "tavily_usage",
		Available:       true,
		Remaining:       &remaining,
		LimitValue:      &limit,
		CheckedAt:       "2026-07-09T00:00:00Z",
		ExpiresAt:       "2026-07-09T00:05:00Z",
		MessageRedacted: &msg,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	page, err := quotas.ProviderPool(keys, providers.ProviderTavily, providers.PoolListOptions{Limit: 25})
	if err != nil {
		t.Fatalf("ProviderPool: %v", err)
	}
	if page.Summary.EnabledKeyCount != 1 || page.Summary.RefreshedKeyCount != 1 {
		t.Fatalf("summary = %#v", page.Summary)
	}
	if len(page.Items) != 1 || page.Items[0].Quota == nil || *page.Items[0].Quota.Remaining != 850 {
		t.Fatalf("items = %#v", page.Items)
	}
}

func TestKeyQuotaRepo_RedactsMessages(t *testing.T) {
	keys, quotas, _ := openKeyQuotaDB(t)
	key, _ := keys.Add(providers.ProviderTavily, "tavily-1", "tvly-secret-123456")
	secret := "Authorization: Bearer tvly-secret-123456"
	if err := quotas.Upsert(providers.ProviderKeyQuota{
		ProviderKeyID:   key.ID,
		Provider:        providers.ProviderTavily,
		Source:          "tavily_usage",
		Available:       false,
		CheckedAt:       "2026-07-09T00:00:00Z",
		ExpiresAt:       "2026-07-09T00:05:00Z",
		MessageRedacted: &secret,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := quotas.Get(key.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.MessageRedacted != nil && strings.Contains(*got.MessageRedacted, "tvly-secret") {
		t.Fatalf("leaked secret: %q", *got.MessageRedacted)
	}
}

func TestKeyQuotaRepo_ProviderPoolPaginationAndCooldown(t *testing.T) {
	keys, quotas, st := openKeyQuotaDB(t)
	_, _ = keys.Add(providers.ProviderTavily, "a", "tvly-a")
	k2, _ := keys.Add(providers.ProviderTavily, "b", "tvly-b")
	until := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	_, _ = st.DB().Exec(`UPDATE provider_keys SET cooldown_until = ?, cooldown_reason = ? WHERE id = ?`, until, "plan_limit_exceeded", k2.ID)
	page, err := quotas.ProviderPool(keys, providers.ProviderTavily, providers.PoolListOptions{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("ProviderPool: %v", err)
	}
	if page.Page.Total != 2 || len(page.Items) != 1 || page.Items[0].Key.ID != k2.ID {
		t.Fatalf("page = %#v", page)
	}
	if page.Summary.CoolingKeyCount != 1 || page.Items[0].Status != providers.PoolKeyStatusCooling {
		t.Fatalf("cooling summary/status = %#v %#v", page.Summary, page.Items[0])
	}
}

func TestProviderPool_ViewEnabledExcludesDisabledAndArchived(t *testing.T) {
	keys, quotas, st := openKeyQuotaDB(t)
	live, err := keys.AddEndpoint(providers.ProviderTavily, "live", "https://api.tavily.com", "tvly-live-key-aaaaaaaa")
	if err != nil {
		t.Fatalf("Add live: %v", err)
	}
	cool, err := keys.AddEndpoint(providers.ProviderTavily, "cool", "https://api.tavily.com", "tvly-cool-key-bbbbbbbb")
	if err != nil {
		t.Fatalf("Add cool: %v", err)
	}
	until := time.Now().UTC().Add(time.Hour)
	reason := "rate_limited"
	if err := keys.MarkFailureWithCooldown(cool.ID, 429, "limited", &until, &reason); err != nil {
		t.Fatalf("MarkFailureWithCooldown: %v", err)
	}
	off, err := keys.AddEndpoint(providers.ProviderTavily, "off", "https://api.tavily.com", "tvly-off-key-cccccccc")
	if err != nil {
		t.Fatalf("Add off: %v", err)
	}
	if err := keys.Disable(off.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	old, err := keys.AddEndpoint(providers.ProviderTavily, "old", "https://api.tavily.com", "tvly-old-key-dddddddd")
	if err != nil {
		t.Fatalf("Add old: %v", err)
	}
	if err := keys.Archive(old.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	_ = st

	enabled, err := quotas.ProviderPool(keys, providers.ProviderTavily, providers.PoolListOptions{
		Limit: 25, View: providers.PoolViewEnabled,
	})
	if err != nil {
		t.Fatalf("ProviderPool enabled: %v", err)
	}
	if enabled.Summary.KeyCount != 4 {
		t.Fatalf("summary.KeyCount = %d want 4 (full pool)", enabled.Summary.KeyCount)
	}
	if enabled.Page.Total != 2 {
		t.Fatalf("page.Total = %d want 2 (eligible only)", enabled.Page.Total)
	}
	if len(enabled.Items) != 2 {
		t.Fatalf("items = %d want 2: %#v", len(enabled.Items), enabled.Items)
	}
	for _, row := range enabled.Items {
		if row.Status == providers.PoolKeyStatusDisabled || row.Status == providers.PoolKeyStatusArchived {
			t.Fatalf("enabled view included %s (%s)", row.Key.Name, row.Status)
		}
	}
	ids := map[int64]bool{enabled.Items[0].Key.ID: true, enabled.Items[1].Key.ID: true}
	if !ids[live.ID] || !ids[cool.ID] {
		t.Fatalf("expected live+cool ids, got %#v", enabled.Items)
	}

	all, err := quotas.ProviderPool(keys, providers.ProviderTavily, providers.PoolListOptions{
		Limit: 25, View: providers.PoolViewAll,
	})
	if err != nil {
		t.Fatalf("ProviderPool all: %v", err)
	}
	if all.Page.Total != 4 || len(all.Items) != 4 {
		t.Fatalf("all view page/items = %d/%d want 4/4", all.Page.Total, len(all.Items))
	}

	// Empty view defaults to enabled.
	def, err := quotas.ProviderPool(keys, providers.ProviderTavily, providers.PoolListOptions{Limit: 25})
	if err != nil {
		t.Fatalf("ProviderPool default: %v", err)
	}
	if def.Page.Total != 2 {
		t.Fatalf("default view total = %d want 2", def.Page.Total)
	}

	// Pagination over eligible-only set.
	p0, err := quotas.ProviderPool(keys, providers.ProviderTavily, providers.PoolListOptions{
		Limit: 1, Offset: 0, View: providers.PoolViewEnabled,
	})
	if err != nil {
		t.Fatalf("page0: %v", err)
	}
	p1, err := quotas.ProviderPool(keys, providers.ProviderTavily, providers.PoolListOptions{
		Limit: 1, Offset: 1, View: providers.PoolViewEnabled,
	})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if p0.Page.Total != 2 || p1.Page.Total != 2 {
		t.Fatalf("paged totals = %d/%d want 2/2", p0.Page.Total, p1.Page.Total)
	}
	if len(p0.Items) != 1 || len(p1.Items) != 1 {
		t.Fatalf("paged item counts = %d/%d", len(p0.Items), len(p1.Items))
	}
	if p0.Items[0].Key.ID == p1.Items[0].Key.ID {
		t.Fatal("both pages returned same key")
	}
	for _, row := range []providers.ProviderPoolRow{p0.Items[0], p1.Items[0]} {
		if row.Status == providers.PoolKeyStatusDisabled || row.Status == providers.PoolKeyStatusArchived {
			t.Fatalf("paginated enabled view leaked %s", row.Status)
		}
	}

	_, err = quotas.ProviderPool(keys, providers.ProviderTavily, providers.PoolListOptions{View: "nope"})
	if err == nil {
		t.Fatal("invalid view: want error")
	}
}
