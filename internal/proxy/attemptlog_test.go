package proxy_test

import (
	"path/filepath"
	"strings"
	"testing"

	"code-guda-gateway/internal/proxy"
	"code-guda-gateway/internal/store"
)

func openAttemptLogDB(t *testing.T) (*proxy.AttemptLogRepo, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return proxy.NewAttemptLogRepo(st.DB(), 3), st
}

func TestAttemptLogRepo_RecordListAndRedact(t *testing.T) {
	repo, _ := openAttemptLogDB(t)
	msg := "Authorization: Bearer tvly-secret"
	if err := repo.Record(proxy.AttemptLog{
		RequestID: "req-1", Provider: "tavily", RouteFamily: "tavily",
		Path: "/tavily/extract", AttemptIndex: 1, StatusClass: "429",
		MessageRedacted: &msg,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	rows, err := repo.List(proxy.AttemptLogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows.Items) != 1 || rows.Items[0].RequestID != "req-1" {
		t.Fatalf("rows = %#v", rows)
	}
	if rows.Items[0].MessageRedacted != nil && strings.Contains(*rows.Items[0].MessageRedacted, "tvly-secret") {
		t.Fatalf("leaked message: %q", *rows.Items[0].MessageRedacted)
	}
}

func TestAttemptLogRepo_RetentionKeepsNewest(t *testing.T) {
	repo, _ := openAttemptLogDB(t)
	for i := 1; i <= 5; i++ {
		if err := repo.Record(proxy.AttemptLog{RequestID: "req", Provider: "tavily", RouteFamily: "tavily", Path: "/tavily/search", AttemptIndex: i, StatusClass: "2xx"}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	rows, err := repo.List(proxy.AttemptLogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if rows.Page.Total != 3 || len(rows.Items) != 3 || rows.Items[0].AttemptIndex != 3 || rows.Items[2].AttemptIndex != 5 {
		t.Fatalf("retention rows = %#v", rows)
	}
}
