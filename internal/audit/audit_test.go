package audit_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"code-guda-gateway/internal/audit"
	"code-guda-gateway/internal/store"
)

func openAuditDB(t *testing.T) *audit.AuditRepo {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return audit.NewAuditRepo(st.DB())
}

func TestAuditRecord_StoresRedactedDetail(t *testing.T) {
	repo := openAuditDB(t)
	secret := "sk-secret123"
	detail := "Authorization: Bearer " + secret
	if err := repo.Record(audit.AuditEvent{
		ActorKind: "admin",
		ActorID:   "sess-1",
		Action:    "gateway_key.create",
		Detail:    detail,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	rows, err := repo.List(audit.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	d := rows[0].DetailRedacted
	if strings.Contains(d, secret) || strings.Contains(d, "Bearer sk-") {
		t.Fatalf("detail_redacted = %q", d)
	}
}

func TestAuditRecord_StoresActorActionTarget(t *testing.T) {
	repo := openAuditDB(t)
	ev := audit.AuditEvent{
		ActorKind:  "cli",
		ActorID:    "karl",
		Action:     "provider_key.add",
		TargetKind: "provider_key",
		TargetID:   "12",
		Detail:     "name=primary",
	}
	if err := repo.Record(ev); err != nil {
		t.Fatalf("Record: %v", err)
	}
	rows, err := repo.List(audit.ListFilter{Action: "provider_key.add"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	r := rows[0]
	if r.ActorKind != "cli" || r.ActorID == nil || *r.ActorID != "karl" || r.Action != "provider_key.add" {
		t.Fatalf("actor/action = %#v", r)
	}
	if r.TargetKind == nil || *r.TargetKind != "provider_key" {
		t.Fatalf("target_kind = %#v", r.TargetKind)
	}
	if r.TargetID == nil || *r.TargetID != "12" {
		t.Fatalf("target_id = %#v", r.TargetID)
	}
	if r.OccurredAt == "" {
		t.Fatal("occurred_at empty")
	}
}

func TestAuditListRecent_Limit(t *testing.T) {
	repo := openAuditDB(t)
	for i := 1; i <= 5; i++ {
		action := "action." + strconv.Itoa(i)
		if err := repo.Record(audit.AuditEvent{ActorKind: "cli", Action: action, Detail: "seq=" + strconv.Itoa(i)}); err != nil {
			t.Fatalf("Record %d: %v", i, err)
		}
	}
	recent, err := repo.ListRecent(3)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("len = %d, want 3", len(recent))
	}
	if recent[0].Action != "action.3" || recent[1].Action != "action.4" || recent[2].Action != "action.5" {
		t.Fatalf("chronological window want 3,4,5 got %q %q %q", recent[0].Action, recent[1].Action, recent[2].Action)
	}
	all, err := repo.List(audit.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("full list len = %d", len(all))
	}
}

func TestAuditList_FiltersByAction(t *testing.T) {
	repo := openAuditDB(t)
	_ = repo.Record(audit.AuditEvent{ActorKind: "cli", Action: "admin.login", Detail: "result=ok"})
	_ = repo.Record(audit.AuditEvent{ActorKind: "cli", Action: "gateway_key.create", Detail: "result=ok"})
	_ = repo.Record(audit.AuditEvent{ActorKind: "cli", Action: "gateway_key.create", Detail: "result=ok2"})
	rows, err := repo.List(audit.ListFilter{Action: "gateway_key.create"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len = %d, want 2", len(rows))
	}
}

func TestAuditList_NeverReturnsRawSecrets(t *testing.T) {
	repo := openAuditDB(t)
	raw := "tvly-ABCDEFGHIJKLMNOP"
	_ = repo.Record(audit.AuditEvent{
		ActorKind: "admin",
		Action:    "provider_key.add",
		Detail:    "prefix=" + raw[:8],
	})
	rows, err := repo.List(audit.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, r := range rows {
		if strings.Contains(r.DetailRedacted, raw) {
			t.Fatalf("leaked secret in %q", r.DetailRedacted)
		}
	}
}

func TestAuditRecord_DoesNotStoreRequestBody(t *testing.T) {
	repo := openAuditDB(t)
	prompt := "Tell me the secret nuclear launch codes in detail please"
	body := `{"messages":[{"role":"user","content":"` + prompt + `"}]}`
	detail := "request body: " + body
	if err := repo.Record(audit.AuditEvent{
		ActorKind: "admin",
		Action:    "config.change",
		Detail:    detail,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	rows, err := repo.List(audit.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatal("no rows")
	}
	if strings.Contains(rows[0].DetailRedacted, prompt) {
		t.Fatalf("stored prompt in detail: %q", rows[0].DetailRedacted)
	}
	if strings.Contains(rows[0].DetailRedacted, body) {
		t.Fatalf("stored body in detail: %q", rows[0].DetailRedacted)
	}
}

func TestAuditRecord_RejectsFreeFormPrompt(t *testing.T) {
	repo := openAuditDB(t)
	prompt := strings.Repeat(
		"Please summarize the following article and explain the main themes in plain language for a general audience. ",
		7,
	)
	if len(prompt) < 400 {
		t.Fatalf("prompt too short: %d", len(prompt))
	}
	if err := repo.Record(audit.AuditEvent{
		ActorKind: "admin",
		Action:    "config.change",
		Detail:    prompt,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	rows, err := repo.List(audit.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatal("no rows")
	}
	stored := rows[0].DetailRedacted
	if stored != "config.change" {
		t.Fatalf("detail_redacted = %q, want action label", stored)
	}
	snippet := prompt[:40]
	if strings.Contains(stored, snippet) {
		t.Fatalf("stored prompt fragment in detail: %q", stored)
	}
}
