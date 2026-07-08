package adminweb_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"code-guda-gateway/internal/adminauth"
	"code-guda-gateway/internal/adminweb"
	"code-guda-gateway/internal/audit"
	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/server"
	"code-guda-gateway/internal/store"
	"code-guda-gateway/internal/usage"
)

func openAdminApp(t *testing.T) (http.Handler, *adminauth.Service, *gatewaykeys.Service, *providers.KeyRepo, *store.Store, []byte) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mkPath := filepath.Join(t.TempDir(), "master.key")
	mk, err := secrets.LoadOrCreate(mkPath)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	gk := gatewaykeys.NewService(st.DB())
	auth := adminauth.NewService(st.DB(), 24*time.Hour)
	keyRepo := providers.NewKeyRepo(st.DB(), mk)
	app := adminweb.New(adminweb.Deps{
		Auth:         auth,
		GatewayKeys:  gk,
		ProviderKeys: keyRepo,
		Settings:     providers.NewSettingsRepo(st.DB()),
		Audit:        audit.NewAuditRepo(st.DB()),
		Usage:        usage.NewUsageRepo(st.DB()),
		Quotas:       providers.NewQuotaRepo(st.DB()),
	})
	return app, auth, gk, keyRepo, st, mk
}

func openAdminAppWithRefresher(t *testing.T, refresher *providers.QuotaRefresher) (http.Handler, *adminauth.Service, *providers.KeyRepo, *store.Store) {
	t.Helper()
	_, auth, gk, keyRepo, st, _ := openAdminApp(t)
	settingsRepo := providers.NewSettingsRepo(st.DB())
	quotaRepo := providers.NewQuotaRepo(st.DB())
	if refresher != nil {
		refresher.ProviderKeys = keyRepo
		refresher.Settings = settingsRepo
		refresher.Quotas = quotaRepo
	}
	app := adminweb.New(adminweb.Deps{
		Auth:           auth,
		GatewayKeys:    gk,
		ProviderKeys:   keyRepo,
		Settings:       settingsRepo,
		Audit:          audit.NewAuditRepo(st.DB()),
		Usage:          usage.NewUsageRepo(st.DB()),
		Quotas:         quotaRepo,
		QuotaRefresher: refresher,
	})
	return app, auth, keyRepo, st
}

func initToken(t *testing.T, auth *adminauth.Service) string {
	t.Helper()
	raw, err := auth.Init()
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return raw
}

func loginSession(t *testing.T, app http.Handler, token string) *http.Cookie {
	t.Helper()
	body := `{"token":"` + token + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", rec.Code, rec.Body.String())
	}
	var c *http.Cookie
	for _, sc := range rec.Result().Cookies() {
		if sc.Name == adminauth.SessionCookieName {
			c = sc
		}
	}
	if c == nil {
		t.Fatal("no session cookie")
	}
	return c
}

func csrfForTest(t *testing.T, app http.Handler, c *http.Cookie) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/admin/api/session", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("session json: %v", err)
	}
	if resp.CSRFToken == "" {
		t.Fatal("empty csrf token")
	}
	return resp.CSRFToken
}

func TestAdminLogin_GETReturnsLoginPage(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	html := rec.Body.String()
	if !strings.Contains(html, `id="token"`) && !strings.Contains(html, "guda-gateway-admin") {
		t.Fatalf("expected login form or init message, got: %s", truncate(html, 200))
	}
	has, _ := auth.HasToken()
	if !has {
		if !strings.Contains(html, "guda-gateway-admin") {
			t.Fatal("expected CLI init message when no token")
		}
		return
	}
	if !strings.Contains(html, `type="password"`) {
		t.Fatal("expected password token input")
	}
}

func TestAdminLogin_POSTValidTokenSetsCookie(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	token := initToken(t, auth)
	body := `{"token":"` + token + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	set := rec.Header().Get("Set-Cookie")
	if !strings.Contains(set, adminauth.SessionCookieName+"=") {
		t.Fatalf("Set-Cookie: %q", set)
	}
	for _, want := range []string{"HttpOnly", "Secure", "SameSite=Lax", "Path=/admin"} {
		if !strings.Contains(set, want) {
			t.Fatalf("Set-Cookie missing %q: %q", want, set)
		}
	}
	var resp struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp.CSRFToken == "" {
		t.Fatal("empty csrf_token")
	}
}

func TestAdminLogin_POSTInvalidTokenReturns401(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	initToken(t, auth)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(`{"token":"gat_bogus1234567890123456789012"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("unexpected cookie on failed login")
	}
}

func TestAdminDashboard_RequiresSession(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	initToken(t, auth)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAdminDashboard_WithSessionReturnsStatus(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	token := initToken(t, auth)
	c := loginSession(t, app, token)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, ok := payload["Providers"]; !ok {
		t.Fatalf("missing Providers: %v", payload)
	}
}

func TestAdminStatic_APINeverFallsBackToSPA(t *testing.T) {
	app, _, _, _, _, _ := openAdminApp(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/does-not-exist", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "<!doctype html") {
		t.Fatal("admin API path fell back to SPA HTML")
	}
}

func TestAdminStatic_MissingAssetsFallbackPage(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Admin UI assets not built") && !strings.Contains(body, `id="root"`) {
		t.Fatalf("expected missing-assets fallback or SPA shell, got %s", truncate(body, 200))
	}
}

func TestAdminAPI_MutatingRequiresCSRF(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/gateway-keys", strings.NewReader(`{"name":"x"}`))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestAdminAPI_MutatingAcceptsSessionCSRF(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/gateway-keys", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminAPI_ErrorShape(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/gateway-keys", strings.NewReader(`{`))
	req.AddCookie(c)
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) || !strings.Contains(rec.Body.String(), `"code"`) {
		t.Fatalf("missing standard error shape: %s", rec.Body.String())
	}
}

func TestGatewayKeys_CreateReturnsRawOnce(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/gateway-keys", strings.NewReader(`{"name":"ops"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d", rec.Code)
	}
	var resp struct {
		RawKey string `json:"raw_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.HasPrefix(resp.RawKey, "gsk_") {
		t.Fatalf("raw_key = %q", resp.RawKey)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/admin/api/gateway-keys", nil)
	req2.AddCookie(c)
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	listBody := rec2.Body.String()
	if strings.Contains(listBody, resp.RawKey) {
		t.Fatal("list response contains raw gateway key")
	}
}

func TestGatewayKeys_ListNeverLeaksRawOrHash(t *testing.T) {
	app, auth, gk, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	raw, _, err := gk.Create("leak-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	hash := sha256.Sum256([]byte(raw))
	hashHex := hex.EncodeToString(hash[:])
	req := httptest.NewRequest(http.MethodGet, "/admin/api/gateway-keys", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, raw) || strings.Contains(body, hashHex) {
		t.Fatal("list leaks raw or full hash")
	}
}

func TestProviderKeys_CreateAcceptsRawReturnsMasked(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	rawProv := "xai-secret-upstream-key-99999999"
	req := httptest.NewRequest(http.MethodPost, "/admin/api/provider-keys", strings.NewReader(
		`{"provider":"grok","name":"primary","key":"`+rawProv+`"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), rawProv) {
		t.Fatal("create response contains raw provider key")
	}
	req2 := httptest.NewRequest(http.MethodGet, "/admin/api/provider-keys", nil)
	req2.AddCookie(c)
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if strings.Contains(rec2.Body.String(), rawProv) {
		t.Fatal("list contains raw provider key")
	}
}

func TestProviderKeys_ListNeverLeaksRawOrCiphertext(t *testing.T) {
	app, auth, _, keyRepo, st, mk := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	rawProv := "tvly-list-leak-test-key-abcdef"
	_, err := keyRepo.Add(providers.ProviderTavily, "t1", rawProv)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	var enc []byte
	if err := st.DB().QueryRow(`SELECT encrypted_key FROM provider_keys LIMIT 1`).Scan(&enc); err != nil {
		t.Fatalf("query enc: %v", err)
	}
	_ = mk
	req := httptest.NewRequest(http.MethodGet, "/admin/api/provider-keys", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, rawProv) || strings.Contains(body, string(enc)) {
		t.Fatal("list leaks raw or ciphertext")
	}
}

func TestProviderKeys_ResetCooldown(t *testing.T) {
	app, auth, _, keyRepo, st, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	d, err := keyRepo.Add(providers.ProviderGrok, "cold", "xai-cooldown-key-1234567890")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	_, err = st.DB().Exec(`UPDATE provider_keys SET cooldown_until = ?, cooldown_reason = ? WHERE id = ?`,
		future, "rate_limited", d.ID)
	if err != nil {
		t.Fatalf("cooldown: %v", err)
	}
	path := "/admin/api/provider-keys/" + strconv.FormatInt(d.ID, 10) + "/reset-cooldown"
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var until sql.NullString
	if err := st.DB().QueryRow(`SELECT cooldown_until FROM provider_keys WHERE id = ?`, d.ID).Scan(&until); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if until.Valid {
		t.Fatalf("cooldown_until still set: %v", until.String)
	}
}

func TestGrokBaseURL_GetAndPatch(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodGet, "/admin/api/providers/grok", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}
	newURL := "https://custom.example/v1"
	req2 := httptest.NewRequest(http.MethodPatch, "/admin/api/providers/grok", strings.NewReader(`{"base_url":"`+newURL+`"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req2.AddCookie(c)
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("patch: %d", rec.Code)
	}
	req3 := httptest.NewRequest(http.MethodGet, "/admin/api/providers/grok", nil)
	req3.AddCookie(c)
	rec3 := httptest.NewRecorder()
	app.ServeHTTP(rec3, req3)
	if !strings.Contains(rec3.Body.String(), newURL) {
		t.Fatalf("get after patch: %s", rec3.Body.String())
	}
}

func TestAdminResourceEndpoints(t *testing.T) {
	app, auth, _, keyRepo, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	csrf := csrfForTest(t, app, c)
	key, err := keyRepo.Add(providers.ProviderGrok, "resource", "xai-resource-key-123456789")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checks := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/admin/api/provider-settings"},
		{method: http.MethodPatch, path: "/admin/api/provider-settings/grok", body: `{"base_url":"https://resource.example/v1"}`},
		{method: http.MethodGet, path: "/admin/api/provider-health"},
		{method: http.MethodPost, path: "/admin/api/providers/grok/test"},
		{method: http.MethodGet, path: "/admin/api/provider-quotas"},
		{method: http.MethodPost, path: "/admin/api/provider-quotas/grok/refresh"},
		{method: http.MethodPost, path: "/admin/api/provider-keys/" + strconv.FormatInt(key.ID, 10) + "/archive"},
		{method: http.MethodPost, path: "/admin/api/provider-keys/" + strconv.FormatInt(key.ID, 10) + "/restore"},
		{method: http.MethodPost, path: "/admin/api/gateway-keys/999/revoke"},
		{method: http.MethodGet, path: "/admin/api/audit-events?action=&actor_kind=&from=&to=&limit=10&offset=0"},
		{method: http.MethodGet, path: "/admin/api/usage-daily?from=&to=&provider=&route_family=&status_class="},
	}
	for _, tc := range checks {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.AddCookie(c)
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if tc.method == http.MethodPost || tc.method == http.MethodPatch || tc.method == http.MethodDelete {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code >= 500 || rec.Code == http.StatusNotFound {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestProviderQuotaUnsupportedShape(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodGet, "/admin/api/provider-quotas", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"provider":"grok"`) ||
		!strings.Contains(rec.Body.String(), `"available":false`) ||
		!strings.Contains(rec.Body.String(), `"source":"unsupported"`) {
		t.Fatalf("unsupported quota shape missing: %s", rec.Body.String())
	}
}

func TestProviderQuotaRefreshTavilyCachesSource(t *testing.T) {
	const testKey = "tvly-test-key-abcdefghij"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":{"usage":150,"limit":1000},"account":{}}`))
	}))
	defer srv.Close()

	ref := &providers.QuotaRefresher{HTTPClient: srv.Client()}
	app, auth, keyRepo, st := openAdminAppWithRefresher(t, ref)
	if err := providers.NewSettingsRepo(st.DB()).SetBaseURL(providers.ProviderTavily, srv.URL); err != nil {
		t.Fatal(err)
	}
	_, err := keyRepo.Add(providers.ProviderTavily, "t1", testKey)
	if err != nil {
		t.Fatal(err)
	}
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/provider-quotas/tavily/refresh", nil)
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, testKey) || strings.Contains(body, "Bearer") {
		t.Fatalf("response leaked secret: %s", body)
	}
	if !strings.Contains(body, `"source":"tavily_usage"`) || !strings.Contains(body, `"available":true`) {
		t.Fatalf("unexpected refresh body: %s", body)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/admin/api/provider-quotas", nil)
	req2.AddCookie(c)
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if !strings.Contains(rec2.Body.String(), `"source":"tavily_usage"`) {
		t.Fatalf("cached list missing source: %s", rec2.Body.String())
	}
}

func TestProviderQuotaRefreshUpstreamFailureStillOK(t *testing.T) {
	const testKey = "tvly-test-key-abcdefghij"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	ref := &providers.QuotaRefresher{HTTPClient: srv.Client()}
	app, auth, keyRepo, st := openAdminAppWithRefresher(t, ref)
	if err := providers.NewSettingsRepo(st.DB()).SetBaseURL(providers.ProviderTavily, srv.URL); err != nil {
		t.Fatal(err)
	}
	_, err := keyRepo.Add(providers.ProviderTavily, "t1", testKey)
	if err != nil {
		t.Fatal(err)
	}
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/provider-quotas/tavily/refresh", nil)
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"available":false`) {
		t.Fatalf("expected unavailable: %s", body)
	}
	if strings.Contains(body, testKey) {
		t.Fatalf("leaked credential: %s", body)
	}
}

func TestAuditEvents_List(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	// audit repo on same DB is inside app - record via login already; add explicit event through second open is wrong.
	// Use server-mounted audit via login + gateway create
	req := httptest.NewRequest(http.MethodPost, "/admin/api/gateway-keys", strings.NewReader(`{"name":"audit"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d", rec.Code)
	}
	var created struct {
		RawKey string `json:"raw_key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("json create: %v", err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/admin/api/audit-events", nil)
	req2.AddCookie(c)
	rec2 := httptest.NewRecorder()
	app.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d", rec2.Code)
	}
	body := rec2.Body.String()
	if !strings.Contains(body, "gateway_key.create") && !strings.Contains(body, "admin.login") {
		t.Fatalf("expected audit actions in %s", body)
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	forbiddenKeys := map[string]bool{
		"raw_key": true, "key_hash": true, "encrypted_key": true, "api_key": true,
	}
	var walk func(any) error
	walk = func(v any) error {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if forbiddenKeys[k] {
					return fmt.Errorf("forbidden field %q in audit JSON", k)
				}
				if err := walk(val); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range x {
				if err := walk(item); err != nil {
					return err
				}
			}
		case string:
			if created.RawKey != "" && strings.Contains(x, created.RawKey) {
				return fmt.Errorf("audit value contains raw gateway key")
			}
			if strings.Contains(x, "gsk_") {
				return fmt.Errorf("audit value contains gateway key prefix")
			}
		}
		return nil
	}
	if err := walk(payload.Items); err != nil {
		t.Fatal(err)
	}
}

func TestUsageDaily_List(t *testing.T) {
	app, auth, gk, _, st, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	_, display, err := gk.Create("usage-test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := display.ID
	usageRepo := usage.NewUsageRepo(st.DB())
	day := usage.DayUTC(time.Now())
	if err := usageRepo.Increment(usage.UsageIncrement{
		Day: day, GatewayKeyID: &id, Provider: providers.ProviderGrok,
		RouteFamily: "grok", StatusClass: "2xx",
	}); err != nil {
		t.Fatalf("Increment: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/api/usage-daily", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var payload struct {
		Items []usage.UsageDaily `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(payload.Items) == 0 || payload.Items[0].RequestCount < 1 {
		t.Fatalf("rows = %#v", payload.Items)
	}
}

func TestAdmin_NoRawSecretsInRenderedHTML(t *testing.T) {
	app, auth, gk, keyRepo, st, _ := openAdminApp(t)
	gwRaw, _, _ := gk.Create("html-test")
	provRaw := "xai-html-leak-key-abcdefghijklmnop"
	_, _ = keyRepo.Add(providers.ProviderGrok, "h", provRaw)
	var enc []byte
	if err := st.DB().QueryRow(`SELECT encrypted_key FROM provider_keys LIMIT 1`).Scan(&enc); err != nil {
		t.Fatalf("query enc: %v", err)
	}
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	html := rec.Body.String()
	sum := sha256.Sum256([]byte(gwRaw))
	hash := hex.EncodeToString(sum[:])
	if strings.Contains(html, gwRaw) || strings.Contains(html, provRaw) || strings.Contains(html, hash) || strings.Contains(html, string(enc)) {
		t.Fatal("dashboard HTML contains raw secrets, gateway hash, or provider ciphertext")
	}
}

func TestAdminLogout_JSONReturnsOK(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp["status"] != "ok" {
		t.Fatalf("resp = %#v", resp)
	}
}

func TestAdminLogout_HTMLFormRedirectsToLogin(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodPost, "/admin/api/logout", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("X-CSRF-Token", csrfForTest(t, app, c))
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if loc != "/admin" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestAdminSession_AuthenticatedWithoutSessionID(t *testing.T) {
	app, auth, _, _, _, _ := openAdminApp(t)
	c := loginSession(t, app, initToken(t, auth))
	req := httptest.NewRequest(http.MethodGet, "/admin/api/session", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if authVal, ok := resp["authenticated"].(bool); !ok || !authVal {
		t.Fatalf("authenticated = %#v", resp)
	}
	if _, ok := resp["session_id"]; ok {
		t.Fatalf("session_id must not be exposed: %#v", resp)
	}
}

func TestServer_MountsAdminRoutes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mk, _ := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "mk"))
	gk := gatewaykeys.NewService(st.DB())
	app := server.New(config.Config{}, gk, st.DB(), mk)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
