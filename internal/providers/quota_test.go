package providers_test

import (
	"path/filepath"
	"testing"
	"time"

	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/store"
)

func openQuotaRepo(t *testing.T) *providers.QuotaRepo {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return providers.NewQuotaRepo(st.DB())
}

func TestQuotaRepoUpsertGetList(t *testing.T) {
	t.Parallel()
	repo := openQuotaRepo(t)
	now := time.Now().UTC()
	used := int64(10)
	limit := int64(100)
	remaining := int64(90)
	msg := "credits from upstream"
	row := providers.QuotaCache{
		Provider:        providers.ProviderTavily,
		Source:          "upstream",
		Available:       true,
		Used:            &used,
		LimitValue:      &limit,
		Remaining:       &remaining,
		CheckedAt:       now.Format(time.RFC3339Nano),
		ExpiresAt:       now.Add(time.Minute).Format(time.RFC3339Nano),
		MessageRedacted: &msg,
	}
	if err := repo.Upsert(row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := repo.Get(providers.ProviderTavily)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || !got.Available || got.Remaining == nil || *got.Remaining != remaining {
		t.Fatalf("got = %#v", got)
	}
	list, err := repo.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Provider != providers.ProviderTavily {
		t.Fatalf("list = %#v", list)
	}
}
