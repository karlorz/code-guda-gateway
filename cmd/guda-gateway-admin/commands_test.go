package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"code-guda-gateway/internal/adminauth"
	"code-guda-gateway/internal/audit"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/store"
)

var (
	reGAT = regexp.MustCompile(`^gat_[A-Za-z0-9]{32}$`)
	reGSK = regexp.MustCompile(`^gsk_[A-Za-z0-9]{32}$`)
)

func testEnv(t *testing.T) (dbPath, masterPath string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "gateway.db"), filepath.Join(dir, "master.key")
}

func runCLI(t *testing.T, dbPath, masterPath string, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	in := strings.NewReader(stdin)
	code = runWithIO(dbPath, masterPath, args, &outBuf, &errBuf, in)
	return outBuf.String(), errBuf.String(), code
}

func openDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s.DB()
}

func loadMaster(t *testing.T, masterPath string) ([]byte, error) {
	t.Helper()
	return secrets.LoadOrCreate(masterPath)
}

func TestTokenInit_PrintsRawTokenOnce(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	stdout, _, code := runCLI(t, dbPath, masterPath, "", "token", "init")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	raw := strings.TrimSpace(stdout)
	if !reGAT.MatchString(raw) {
		t.Fatalf("stdout token %q does not match gat_<32>", raw)
	}
	db := openDB(t, dbPath)
	var hash string
	if err := db.QueryRow(`SELECT token_hash FROM admin_tokens LIMIT 1`).Scan(&hash); err != nil {
		t.Fatalf("token_hash: %v", err)
	}
	if hash == "" || hash == raw {
		t.Fatalf("bad hash in DB")
	}
}

func TestTokenRotate_PrintsNewRawInvalidatesOld(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	stdout1, _, _ := runCLI(t, dbPath, masterPath, "", "token", "init")
	oldRaw := strings.TrimSpace(stdout1)
	stdout2, _, code := runCLI(t, dbPath, masterPath, "", "token", "rotate")
	if code != 0 {
		t.Fatalf("rotate exit %d", code)
	}
	newRaw := strings.TrimSpace(stdout2)
	if newRaw == oldRaw || !reGAT.MatchString(newRaw) {
		t.Fatalf("new raw %q", newRaw)
	}
	auth := adminauth.NewService(openDB(t, dbPath), 0)
	ok, _ := auth.Verify(oldRaw)
	if ok {
		t.Fatal("old token still valid")
	}
	ok, _ = auth.Verify(newRaw)
	if !ok {
		t.Fatal("new token not valid")
	}
}

func TestTokenInit_SaveEnvWritesTokenToDevSecretFile(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	envPath := filepath.Join(t.TempDir(), "secrets", "guda-gateway.env")
	stdout, stderr, code := runCLI(t, dbPath, masterPath, "", "token", "init", "--save-env", envPath)
	if code != 0 {
		t.Fatalf("exit %d stderr=%s", code, stderr)
	}
	raw := strings.TrimSpace(stdout)
	if !reGAT.MatchString(raw) {
		t.Fatalf("stdout token %q does not match gat_<32>", raw)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "GUDA_ADMIN_TOKEN="+raw {
		t.Fatalf("env file = %q", got)
	}
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat env: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("env file mode = %v, want 0600", got)
	}
}

func TestTokenRotate_SaveEnvReplacesExistingBindingOnly(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	dir := t.TempDir()
	envPath := filepath.Join(dir, "guda-gateway.env")
	if err := os.WriteFile(envPath, []byte("DB_PATH=/tmp/dev.db\nGUDA_ADMIN_TOKEN=old-token\nGUDA_API_KEY=gsk_keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _ = runCLI(t, dbPath, masterPath, "", "token", "init")
	stdout, stderr, code := runCLI(t, dbPath, masterPath, "", "token", "rotate", "--save-env", envPath)
	if code != 0 {
		t.Fatalf("rotate exit %d stderr=%s", code, stderr)
	}
	raw := strings.TrimSpace(stdout)
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "DB_PATH=/tmp/dev.db\n") || !strings.Contains(got, "GUDA_API_KEY=gsk_keep\n") {
		t.Fatalf("env file did not preserve existing bindings: %q", got)
	}
	if strings.Contains(got, "old-token") {
		t.Fatalf("env file still contains old token: %q", got)
	}
	if !strings.Contains(got, "GUDA_ADMIN_TOKEN="+raw+"\n") {
		t.Fatalf("env file missing new token binding: %q", got)
	}
}

func TestTokenInit_RefusesDoubleInit(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	stdout1, stderr1, code1 := runCLI(t, dbPath, masterPath, "", "token", "init")
	if code1 != 0 {
		t.Fatalf("first init exit %d stderr=%s", code1, stderr1)
	}
	firstRaw := strings.TrimSpace(stdout1)
	if !reGAT.MatchString(firstRaw) {
		t.Fatalf("first token %q", firstRaw)
	}
	_, stderr2, code2 := runCLI(t, dbPath, masterPath, "", "token", "init")
	if code2 == 0 {
		t.Fatal("second init should fail")
	}
	if !strings.Contains(stderr2, "already initialized") {
		t.Fatalf("stderr want already initialized: %q", stderr2)
	}
	auth := adminauth.NewService(openDB(t, dbPath), 0)
	ok, err := auth.Verify(firstRaw)
	if err != nil {
		t.Fatalf("verify first: %v", err)
	}
	if !ok {
		t.Fatal("first token no longer valid after refused second init")
	}
}

func TestTokenVerify_ValidAndInvalid(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	stdout, _, _ := runCLI(t, dbPath, masterPath, "", "token", "init")
	raw := strings.TrimSpace(stdout)
	out, _, code := runCLI(t, dbPath, masterPath, raw+"\n", "token", "verify")
	if code != 0 || strings.TrimSpace(out) != "valid" {
		t.Fatalf("valid: code=%d out=%q", code, out)
	}
	out, _, code = runCLI(t, dbPath, masterPath, "bogus\n", "token", "verify")
	if code != 0 || strings.TrimSpace(out) != "invalid" {
		t.Fatalf("invalid: code=%d out=%q", code, out)
	}
}

func TestGatewayKeyCreate_PrintsRawOnce(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	stdout, _, code := runCLI(t, dbPath, masterPath, "", "gateway-key", "create", "--name", "cli-test")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	raw := strings.TrimSpace(stdout)
	if !reGSK.MatchString(raw) {
		t.Fatalf("stdout %q", raw)
	}
	db := openDB(t, dbPath)
	var hash string
	if err := db.QueryRow(`SELECT key_hash FROM gateway_keys LIMIT 1`).Scan(&hash); err != nil {
		t.Fatalf("key_hash: %v", err)
	}
	if hash == raw {
		t.Fatal("raw in DB as hash")
	}
}

func TestGatewayKeyList_MaskedOnly(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	r1, _, _ := runCLI(t, dbPath, masterPath, "", "gateway-key", "create", "--name", "a")
	r2, _, _ := runCLI(t, dbPath, masterPath, "", "gateway-key", "create", "--name", "b")
	raw1, raw2 := strings.TrimSpace(r1), strings.TrimSpace(r2)
	list, _, code := runCLI(t, dbPath, masterPath, "", "gateway-key", "list")
	if code != 0 {
		t.Fatalf("list exit %d", code)
	}
	if !strings.Contains(list, "gsk_") && !strings.Contains(list, "a") {
		t.Fatalf("list missing expected fields: %q", list)
	}
	if strings.Contains(list, raw1) || strings.Contains(list, raw2) {
		t.Fatalf("list contains raw key")
	}
	db := openDB(t, dbPath)
	var hash string
	_ = db.QueryRow(`SELECT key_hash FROM gateway_keys LIMIT 1`).Scan(&hash)
	if strings.Contains(list, hash) {
		t.Fatal("list contains full hash")
	}
}

func TestGatewayKeyDisableEnableRevokeDelete(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	rawOut, _, _ := runCLI(t, dbPath, masterPath, "", "gateway-key", "create", "--name", "life")
	raw := strings.TrimSpace(rawOut)
	gk := gatewaykeys.NewService(openDB(t, dbPath))
	keys, _ := gk.List()
	if len(keys) != 1 {
		t.Fatalf("keys %d", len(keys))
	}
	id := keys[0].ID
	if _, _, code := runCLI(t, dbPath, masterPath, "", "gateway-key", "disable", "--id", strconv.FormatInt(id, 10)); code != 0 {
		t.Fatalf("disable")
	}
	if d, err := gk.Verify(raw); err != gatewaykeys.ErrNotAuthorized || d != nil {
		t.Fatalf("disabled key verify: d=%v err=%v", d, err)
	}
	if _, _, code := runCLI(t, dbPath, masterPath, "", "gateway-key", "enable", "--id", strconv.FormatInt(id, 10)); code != 0 {
		t.Fatalf("enable")
	}
	if _, _, code := runCLI(t, dbPath, masterPath, "", "gateway-key", "revoke", "--id", strconv.FormatInt(id, 10)); code != 0 {
		t.Fatalf("revoke")
	}
	if _, _, code := runCLI(t, dbPath, masterPath, "", "gateway-key", "delete", "--id", strconv.FormatInt(id, 10)); code != 0 {
		t.Fatalf("delete")
	}
	keys, _ = gk.List()
	if len(keys) != 0 {
		t.Fatalf("after delete len=%d", len(keys))
	}
}

func TestProviderKeyAdd_EmptyStdinFails(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	_, stderr, code := runCLI(t, dbPath, masterPath, "", "provider-key", "add", "--provider", "grok", "--name", "primary")
	if code == 0 {
		t.Fatal("empty stdin should fail")
	}
	if !strings.Contains(stderr, "empty provider key") {
		t.Fatalf("stderr: %q", stderr)
	}
}

func TestProviderKeyAdd_RejectsOrIgnoresArgvKey(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	argvSecret := "argv-secret-should-not-be-used"
	stdinSecret := "stdin-secret-correct-key\n"
	stdout, _, code := runCLI(t, dbPath, masterPath, stdinSecret,
		"provider-key", "add", "--provider", "grok", "--name", "from-stdin", argvSecret)
	if code != 0 {
		t.Fatalf("add with stdin exit %d stdout=%s", code, stdout)
	}
	if strings.Contains(stdout, argvSecret) {
		t.Fatal("stdout leaked argv secret")
	}
	mk, err := loadMaster(t, masterPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := providers.NewKeyRepo(openDB(t, dbPath), mk)
	_, rawStored, err := repo.SelectKey("grok")
	if err != nil {
		t.Fatalf("SelectKey: %v", err)
	}
	if rawStored != strings.TrimSpace(stdinSecret) {
		t.Fatalf("stored key from stdin want %q got %q", strings.TrimSpace(stdinSecret), rawStored)
	}
	if rawStored == argvSecret {
		t.Fatal("stored argv secret instead of stdin")
	}
}

func TestProviderKeyAdd_ReadsStdinMasksOutput(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	secret := "xai-secret-key-material-12345"
	stdout, _, code := runCLI(t, dbPath, masterPath, secret+"\n", "provider-key", "add", "--provider", "grok", "--name", "primary")
	if code != 0 {
		t.Fatalf("add exit %d stdout=%s", code, stdout)
	}
	if strings.Contains(stdout, secret) {
		t.Fatalf("stdout leaked raw key: %q", stdout)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatalf("expected confirmation output")
	}
	db := openDB(t, dbPath)
	var enc []byte
	if err := db.QueryRow(`SELECT encrypted_key FROM provider_keys LIMIT 1`).Scan(&enc); err != nil {
		t.Fatalf("encrypted_key: %v", err)
	}
	if string(enc) == secret {
		t.Fatal("plaintext in DB")
	}
}

func TestProviderKeyList_MaskedOnly(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	secret := "tvly-abcdefghijklmnop"
	_, _, _ = runCLI(t, dbPath, masterPath, secret+"\n", "provider-key", "add", "--provider", "tavily", "--name", "t1")
	list, _, code := runCLI(t, dbPath, masterPath, "", "provider-key", "list")
	if code != 0 {
		t.Fatalf("list exit %d", code)
	}
	if strings.Contains(list, secret) {
		t.Fatal("list contains raw key")
	}
	if !strings.Contains(list, "tavily") {
		t.Fatalf("list: %q", list)
	}
}

func TestProviderKeyDisableEnableResetCooldownDelete(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	_, _, _ = runCLI(t, dbPath, masterPath, "key-one\n", "provider-key", "add", "--provider", "grok", "--name", "k")
	mk, err := loadMaster(t, masterPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := providers.NewKeyRepo(openDB(t, dbPath), mk)
	all, _ := repo.ListAll()
	if len(all) != 1 {
		t.Fatalf("keys %d", len(all))
	}
	id := all[0].ID
	for _, args := range [][]string{
		{"provider-key", "disable", "--id", strconv.FormatInt(id, 10)},
		{"provider-key", "enable", "--id", strconv.FormatInt(id, 10)},
	} {
		if _, _, c := runCLI(t, dbPath, masterPath, "", args...); c != 0 {
			t.Fatalf("cmd %v", args)
		}
	}
	db := openDB(t, dbPath)
	now := "2099-01-01T00:00:00Z"
	_, _ = db.Exec(`UPDATE provider_keys SET cooldown_until = ?, cooldown_reason = ? WHERE id = ?`, now, "test", id)
	if _, _, c := runCLI(t, dbPath, masterPath, "", "provider-key", "reset-cooldown", "--id", strconv.FormatInt(id, 10)); c != 0 {
		t.Fatalf("reset-cooldown")
	}
	got, _ := repo.Get(id)
	if got.CooldownUntil != nil {
		t.Fatal("cooldown still set")
	}
	if _, _, c := runCLI(t, dbPath, masterPath, "", "provider-key", "delete", "--id", strconv.FormatInt(id, 10)); c != 0 {
		t.Fatalf("delete")
	}
	all, _ = repo.ListAll()
	if len(all) != 0 {
		t.Fatalf("after delete %d", len(all))
	}
}

func TestGrokSetBaseURL_GetBaseURL(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	url := "https://custom.grok.example/v1"
	if _, _, c := runCLI(t, dbPath, masterPath, "", "grok", "set-base-url", url); c != 0 {
		t.Fatalf("set")
	}
	out, _, c := runCLI(t, dbPath, masterPath, "", "grok", "get-base-url")
	if c != 0 || strings.TrimSpace(out) != url {
		t.Fatalf("get: code=%d out=%q", c, out)
	}
}

func TestGrokQuotaSettingsCLI(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")

	// Test quota-mode
	if _, _, c := runCLI(t, dbPath, masterPath, "", "grok", "set-quota-mode", "grok2api_admin"); c != 0 {
		t.Fatalf("set-quota-mode failed")
	}
	out, _, c := runCLI(t, dbPath, masterPath, "", "grok", "get-quota-mode")
	if c != 0 || strings.TrimSpace(out) != "grok2api_admin" {
		t.Fatalf("get-quota-mode: code=%d out=%q", c, out)
	}

	// Test admin-base-url
	url := "http://127.0.0.1:9000"
	if _, _, c := runCLI(t, dbPath, masterPath, "", "grok", "set-admin-base-url", url); c != 0 {
		t.Fatalf("set-admin-base-url failed")
	}
	out, _, c = runCLI(t, dbPath, masterPath, "", "grok", "get-admin-base-url")
	if c != 0 || strings.TrimSpace(out) != url {
		t.Fatalf("get-admin-base-url: code=%d out=%q", c, out)
	}

	// Test admin-key
	if _, _, c := runCLI(t, dbPath, masterPath, "super-secret-admin-key", "grok", "set-admin-key"); c != 0 {
		t.Fatalf("set-admin-key failed")
	}
	// Verify that it is encrypted in settings table
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	settings := providers.NewSettingsRepo(st.DB())
	mk, err := secrets.LoadOrCreate(masterPath)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := settings.GetGrok2APIAdminKey(mk)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "super-secret-admin-key" {
		t.Fatalf("expected decrypted key = super-secret-admin-key, got %q", decrypted)
	}
}

func TestAuditTail_PrintsRedacted(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	rawTok, _, _ := runCLI(t, dbPath, masterPath, "", "token", "init")
	rawTok = strings.TrimSpace(rawTok)
	auditRepo := audit.NewAuditRepo(openDB(t, dbPath))
	_ = auditRepo.Record(audit.AuditEvent{
		ActorKind: "cli", Action: "test_action", Detail: "name=safe;id=1",
	})
	out, _, code := runCLI(t, dbPath, masterPath, "", "audit", "tail", "--limit", "5")
	if code != 0 {
		t.Fatalf("tail exit %d", code)
	}
	if !strings.Contains(out, "test_action") {
		t.Fatalf("missing action: %q", out)
	}
	if strings.Contains(out, rawTok) {
		t.Fatal("audit tail leaked token")
	}
}

func TestCLI_StateChangingCommandsAuditWithoutSecrets(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	if _, _, c := runCLI(t, dbPath, masterPath, "", "db", "migrate"); c != 0 {
		t.Fatal("migrate")
	}
	rawTokenOut, _, c := runCLI(t, dbPath, masterPath, "", "token", "init")
	if c != 0 {
		t.Fatal("token init")
	}
	rawGatewayOut, _, c := runCLI(t, dbPath, masterPath, "", "gateway-key", "create", "--name", "audited")
	if c != 0 {
		t.Fatal("gateway-key create")
	}
	rawProvider := "Bearer sk-provider-secret-123456789"
	if _, _, c := runCLI(t, dbPath, masterPath, rawProvider+"\n", "provider-key", "add", "--provider", "grok", "--name", "audited"); c != 0 {
		t.Fatal("provider-key add")
	}
	if _, _, c := runCLI(t, dbPath, masterPath, "", "grok", "set-base-url", "https://audit.example/v1"); c != 0 {
		t.Fatal("grok set-base-url")
	}
	events, err := audit.NewAuditRepo(openDB(t, dbPath)).List(audit.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	actions := map[string]bool{}
	var details strings.Builder
	for _, ev := range events {
		actions[ev.Action] = true
		details.WriteString(ev.DetailRedacted)
		details.WriteString("\n")
	}
	for _, want := range []string{"db.migrate", "admin_token.init", "gateway_key.create", "provider_key.add", "provider_setting.update"} {
		if !actions[want] {
			t.Fatalf("missing audit action %q in %#v", want, actions)
		}
	}
	detail := details.String()
	for _, forbidden := range []string{strings.TrimSpace(rawTokenOut), strings.TrimSpace(rawGatewayOut), rawProvider, "Bearer ", "sk-provider-secret"} {
		if forbidden != "" && strings.Contains(detail, forbidden) {
			t.Fatalf("audit detail leaked %q in %q", forbidden, detail)
		}
	}
}

func TestDBMigrate_CreatesSchema(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	out, _, code := runCLI(t, dbPath, masterPath, "", "db", "migrate")
	if code != 0 {
		t.Fatalf("migrate exit %d: %s", code, out)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tbl := range []string{"admin_tokens", "gateway_keys", "provider_keys", "audit_events"} {
		var n int
		err := s.DB().QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl,
		).Scan(&n)
		if err != nil || n != 1 {
			t.Fatalf("table %s missing", tbl)
		}
	}
}

func TestCLI_RecoveryWithoutService(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	if _, _, c := runCLI(t, dbPath, masterPath, "", "db", "migrate"); c != 0 {
		t.Fatal("migrate")
	}
	tokOut, _, c := runCLI(t, dbPath, masterPath, "", "token", "init")
	if c != 0 {
		t.Fatal("token init")
	}
	tok := strings.TrimSpace(tokOut)
	gkOut, _, c := runCLI(t, dbPath, masterPath, "", "gateway-key", "create", "--name", "recovery")
	if c != 0 {
		t.Fatal("gateway-key create")
	}
	_ = strings.TrimSpace(gkOut)
	if _, _, c := runCLI(t, dbPath, masterPath, "upstream-secret\n", "provider-key", "add", "--provider", "firecrawl", "--name", "fc1"); c != 0 {
		t.Fatal("provider-key add")
	}
	verifyOut, _, c := runCLI(t, dbPath, masterPath, tok+"\n", "token", "verify")
	if c != 0 || strings.TrimSpace(verifyOut) != "valid" {
		t.Fatalf("verify: %q", verifyOut)
	}
}


func TestProviderEndpointAdd_RequiresBaseURLAndMasksSecret(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	secret := "tvly-cli-endpoint-secret-abcdef"
	baseURL := "https://proxy.example/tavily"
	stdout, stderr, code := runCLI(t, dbPath, masterPath, secret+"\n",
		"provider-endpoint", "add",
		"--provider", "tavily",
		"--name", "ep1",
		"--base-url", baseURL,
	)
	if code != 0 {
		t.Fatalf("add exit %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if strings.Contains(stdout, secret) {
		t.Fatalf("stdout leaked secret: %q", stdout)
	}
	if !strings.Contains(stdout, "base_url=") && !strings.Contains(stdout, "proxy.example/tavily") {
		t.Fatalf("stdout missing base_url: %q", stdout)
	}
	mk, err := loadMaster(t, masterPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := providers.NewKeyRepo(openDB(t, dbPath), mk)
	all, err := repo.ListAll()
	if err != nil || len(all) != 1 {
		t.Fatalf("list: len=%d err=%v", len(all), err)
	}
	if !strings.Contains(all[0].BaseURL, "proxy.example/tavily") {
		t.Fatalf("stored BaseURL=%q", all[0].BaseURL)
	}
}

func TestProviderEndpointList_ShowsBaseURLColumn(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	secret := "xai-list-endpoint-key-123456"
	_, _, code := runCLI(t, dbPath, masterPath, secret+"\n",
		"provider-endpoint", "add",
		"--provider", "grok",
		"--name", "g1",
		"--base-url", "https://api.x.ai/v1",
	)
	if code != 0 {
		t.Fatalf("add exit %d", code)
	}
	list, _, code := runCLI(t, dbPath, masterPath, "", "provider-endpoint", "list")
	if code != 0 {
		t.Fatalf("list exit %d", code)
	}
	if strings.Contains(list, secret) {
		t.Fatal("list leaked secret")
	}
	if !strings.Contains(list, "BASE_URL") {
		t.Fatalf("list missing BASE_URL column: %q", list)
	}
	if !strings.Contains(list, "api.x.ai") {
		t.Fatalf("list missing base url value: %q", list)
	}
}

func TestProviderEndpoint_SetBaseURLAndRotateKey(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	oldKey := "xai-old-cli-key-1111111111"
	stdout, _, code := runCLI(t, dbPath, masterPath, oldKey+"\n",
		"provider-endpoint", "add",
		"--provider", "grok",
		"--name", "rot",
		"--base-url", "https://api.x.ai/v1",
	)
	if code != 0 {
		t.Fatalf("add exit %d out=%s", code, stdout)
	}
	mk, err := loadMaster(t, masterPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := providers.NewKeyRepo(openDB(t, dbPath), mk)
	all, _ := repo.ListAll()
	if len(all) != 1 {
		t.Fatalf("keys %d", len(all))
	}
	id := all[0].ID
	idStr := strconv.FormatInt(id, 10)

	newURL := "https://custom.cli.example/v1"
	if _, _, c := runCLI(t, dbPath, masterPath, "", "provider-endpoint", "set-base-url", "--id", idStr, "--url", newURL); c != 0 {
		t.Fatal("set-base-url")
	}
	got, _ := repo.Get(id)
	if got.BaseURL != newURL {
		t.Fatalf("BaseURL=%q want %q", got.BaseURL, newURL)
	}

	newKey := "xai-new-cli-key-2222222222"
	rotOut, _, c := runCLI(t, dbPath, masterPath, newKey+"\n", "provider-endpoint", "rotate-key", "--id", idStr)
	if c != 0 {
		t.Fatalf("rotate-key exit %d out=%s", c, rotOut)
	}
	if strings.Contains(rotOut, oldKey) || strings.Contains(rotOut, newKey) {
		t.Fatal("rotate-key output leaked secret")
	}
	raw, err := repo.RawKey(id)
	if err != nil {
		t.Fatalf("RawKey: %v", err)
	}
	if raw != newKey {
		t.Fatalf("stored=%q want %q", raw, newKey)
	}
}

func TestLegacyProviderKey_AddStillUsesDefaultBaseURL(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	secret := "xai-legacy-cli-key-33333333"
	if _, _, c := runCLI(t, dbPath, masterPath, secret+"\n", "provider-key", "add", "--provider", "grok", "--name", "legacy"); c != 0 {
		t.Fatal("provider-key add")
	}
	mk, err := loadMaster(t, masterPath)
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t, dbPath)
	repo := providers.NewKeyRepo(db, mk)
	defaultURL, err := providers.NewSettingsRepo(db).GetBaseURL(providers.ProviderGrok)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := repo.ListAll()
	if len(all) != 1 {
		t.Fatalf("keys %d", len(all))
	}
	if all[0].BaseURL != defaultURL {
		t.Fatalf("BaseURL=%q want default %q", all[0].BaseURL, defaultURL)
	}
}

func TestProviderEndpoint_AndLegacyProviderKey_SameRowMutations(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	secret := "xai-same-row-cli-key-444444"
	if _, _, c := runCLI(t, dbPath, masterPath, secret+"\n",
		"provider-endpoint", "add",
		"--provider", "grok",
		"--name", "same",
		"--base-url", "https://api.x.ai/v1",
	); c != 0 {
		t.Fatal("provider-endpoint add")
	}
	mk, err := loadMaster(t, masterPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := providers.NewKeyRepo(openDB(t, dbPath), mk)
	all, _ := repo.ListAll()
	id := all[0].ID
	idStr := strconv.FormatInt(id, 10)

	if _, _, c := runCLI(t, dbPath, masterPath, "", "provider-key", "disable", "--id", idStr); c != 0 {
		t.Fatal("legacy disable")
	}
	got, _ := repo.Get(id)
	if got.Enabled {
		t.Fatal("still enabled")
	}
	if _, _, c := runCLI(t, dbPath, masterPath, "", "provider-endpoint", "enable", "--id", idStr); c != 0 {
		t.Fatal("canonical enable")
	}
	got, _ = repo.Get(id)
	if !got.Enabled {
		t.Fatal("still disabled")
	}
	if _, _, c := runCLI(t, dbPath, masterPath, "", "provider-key", "demote", "--id", idStr); c != 0 {
		t.Fatal("legacy demote")
	}
	got, _ = repo.Get(id)
	if got.LastFailedAt == nil {
		t.Fatal("expected last_failed_at")
	}
	if _, _, c := runCLI(t, dbPath, masterPath, "", "provider-endpoint", "reset-selection", "--id", idStr); c != 0 {
		t.Fatal("canonical reset-selection")
	}
	got, _ = repo.Get(id)
	if got.LastFailedAt != nil {
		t.Fatal("last_failed_at still set")
	}
}

func TestProviderEndpoint_AuditNoSecrets(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")
	secret := "tvly-cli-audit-secret-zzzz"
	if _, _, c := runCLI(t, dbPath, masterPath, secret+"\n",
		"provider-endpoint", "add",
		"--provider", "tavily",
		"--name", "aud",
		"--base-url", "https://api.tavily.com",
	); c != 0 {
		t.Fatal("add")
	}
	mk, _ := loadMaster(t, masterPath)
	repo := providers.NewKeyRepo(openDB(t, dbPath), mk)
	all, _ := repo.ListAll()
	idStr := strconv.FormatInt(all[0].ID, 10)
	newKey := "tvly-cli-audit-rotated-yyyy"
	if _, _, c := runCLI(t, dbPath, masterPath, newKey+"\n", "provider-endpoint", "rotate-key", "--id", idStr); c != 0 {
		t.Fatal("rotate-key")
	}
	events, err := audit.NewAuditRepo(openDB(t, dbPath)).List(audit.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var details strings.Builder
	actions := map[string]bool{}
	for _, ev := range events {
		actions[ev.Action] = true
		details.WriteString(ev.DetailRedacted)
		details.WriteString("\n")
	}
	if !actions["provider_endpoint.add"] && !actions["provider_key.add"] {
		t.Fatalf("missing add audit in %#v", actions)
	}
	detail := details.String()
	if strings.Contains(detail, secret) || strings.Contains(detail, newKey) {
		t.Fatalf("audit detail leaked secret: %q", detail)
	}
}

// TestSeedProviderKeysScript_PassesURLAndKeepsSecretsOffArgv asserts the shared-base
// seed script uses canonical provider-endpoint add with an explicit --base-url for
// every row and never places raw keys on argv (stdin only).
func TestSeedProviderKeysScript_PassesURLAndKeepsSecretsOffArgv(t *testing.T) {
	// Resolve seed script relative to module root (this package is cmd/guda-gateway-admin).
	root := filepath.Clean(filepath.Join("..", ".."))
	scriptPath := filepath.Join(root, "scripts", "seed-provider-keys.sh")
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read seed script: %v", err)
	}
	src := string(raw)

	if !strings.Contains(src, "provider-endpoint") || !strings.Contains(src, "add") {
		t.Fatal("seed script must call provider-endpoint add")
	}
	if !strings.Contains(src, "--base-url") {
		t.Fatal("seed script must pass --base-url into every created row")
	}
	// Prefer canonical command; legacy provider-key add is not enough for explicit URLs.
	if strings.Contains(src, "provider-key add") {
		t.Fatal("seed script still uses legacy provider-key add; switch to provider-endpoint add --base-url")
	}
	// Keys must flow via stdin (printf | admin), never as --key / trailing argv.
	if strings.Contains(src, "--key ") || strings.Contains(src, "--api-key") {
		t.Fatal("seed script must not pass raw keys as CLI flags")
	}
	if !strings.Contains(src, "printf") || !strings.Contains(src, "|") {
		t.Fatal("seed script must pipe keys via stdin")
	}
	// Env secret names must not be interpolated into the admin argv string beyond
	// the local key variable that is only written to the pipe.
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "provider-endpoint") && strings.Contains(line, "GROK_API_KEY") {
			t.Fatalf("raw env key appears on provider-endpoint argv line: %s", line)
		}
		if strings.Contains(line, "provider-endpoint") && strings.Contains(line, "FIRECRAWL_API_KEY") {
			t.Fatalf("raw env key appears on provider-endpoint argv line: %s", line)
		}
		if strings.Contains(line, "provider-endpoint") && strings.Contains(line, "TAVILY_API_KEYS") {
			t.Fatalf("raw env key appears on provider-endpoint argv line: %s", line)
		}
	}
}

// TestSeedStyleSharedBase_CreatesRowsWithBaseURLAndNoSecretLeak simulates the
// shared-base seed path: one explicit base URL per provider, key on stdin only.
func TestSeedStyleSharedBase_CreatesRowsWithBaseURLAndNoSecretLeak(t *testing.T) {
	dbPath, masterPath := testEnv(t)
	_, _, _ = runCLI(t, dbPath, masterPath, "", "db", "migrate")

	type row struct {
		provider, name, baseURL, secret string
	}
	rows := []row{
		{"grok", "grok2api", providers.DefaultGrokBaseURL, "xai-seed-style-secret-grok111"},
		{"firecrawl", "gh01", providers.DefaultFirecrawlBaseURL, "fc-seed-style-secret-fire222"},
		{"tavily", "tavily-1", providers.DefaultTavilyBaseURL, "tvly-seed-style-secret-tav333"},
		{"tavily", "tavily-2", providers.DefaultTavilyBaseURL, "tvly-seed-style-secret-tav444"},
	}
	var combinedOut strings.Builder
	for _, r := range rows {
		stdout, stderr, code := runCLI(t, dbPath, masterPath, r.secret+"\n",
			"provider-endpoint", "add",
			"--provider", r.provider,
			"--name", r.name,
			"--base-url", r.baseURL,
		)
		if code != 0 {
			t.Fatalf("add %s/%s exit %d stderr=%s stdout=%s", r.provider, r.name, code, stderr, stdout)
		}
		combinedOut.WriteString(stdout)
		combinedOut.WriteString(stderr)
		if strings.Contains(stdout, r.secret) || strings.Contains(stderr, r.secret) {
			t.Fatalf("output leaked secret for %s/%s", r.provider, r.name)
		}
	}

	list, _, code := runCLI(t, dbPath, masterPath, "", "provider-endpoint", "list")
	if code != 0 {
		t.Fatalf("list exit %d", code)
	}
	combinedOut.WriteString(list)
	for _, r := range rows {
		if strings.Contains(list, r.secret) || strings.Contains(combinedOut.String(), r.secret) {
			t.Fatalf("list/output leaked secret for %s", r.name)
		}
	}

	mk, err := loadMaster(t, masterPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := providers.NewKeyRepo(openDB(t, dbPath), mk)
	all, err := repo.ListAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(rows) {
		t.Fatalf("rows=%d want %d", len(all), len(rows))
	}
	byName := map[string]string{}
	for _, d := range all {
		byName[d.Provider+"/"+d.Name] = d.BaseURL
		if strings.TrimSpace(d.BaseURL) == "" {
			t.Fatalf("empty base_url for %s/%s", d.Provider, d.Name)
		}
	}
	for _, r := range rows {
		got, ok := byName[r.provider+"/"+r.name]
		if !ok {
			t.Fatalf("missing row %s/%s", r.provider, r.name)
		}
		if got != r.baseURL {
			t.Fatalf("%s/%s base_url=%q want %q", r.provider, r.name, got, r.baseURL)
		}
	}
}
