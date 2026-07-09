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
