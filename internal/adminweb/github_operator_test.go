package adminweb_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-guda-gateway/internal/adminweb"
	"code-guda-gateway/internal/audit"
	"code-guda-gateway/internal/operatorlogin"
)

func TestGitHubSettingsRequiresAdminCSRFAndNeverReturnsSecret(t *testing.T) {
	_, auth, _, _, st, key := openAdminApp(t)
	app := adminweb.New(adminweb.Deps{Auth: auth, GitHub: operatorlogin.New(st.DB(), key, "https://gateway.test"), Audit: audit.NewAuditRepo(st.DB())})
	token := initToken(t, auth)
	cookie := loginSession(t, app, token)
	csrf := csrfForTest(t, app, cookie)
	path := "/admin/api/settings/github-operator"
	body := `{"enabled":true,"client_id":"test-app","client_secret":"test-secret-value","allowed_user_ids":["42"]}`
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 401 {
		t.Fatal("anonymous settings read allowed")
	}
	if w = serveMutatingAdmin(app, "PATCH", path, body, "", cookie); w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	w = serveMutatingAdmin(app, "PATCH", path, body, csrf, cookie)
	if w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "test-secret-value") || strings.Contains(w.Body.String(), "secret_ciphertext") {
		t.Fatal("secret returned")
	}
	var status operatorlogin.Status
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || !status.Enabled || !status.SecretConfigured || status.CallbackURL != "https://gateway.test"+operatorlogin.CallbackPath {
		t.Fatal("incorrect status")
	}
	req := httptest.NewRequest("GET", path, nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), "test-secret-value") {
		t.Fatal("unsafe settings read")
	}
	w = serveMutatingAdmin(app, "PATCH", path, `{"enabled":true,"client_id":"test-app","client_secret":"","allowed_user_ids":["42"]}`, csrf, cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"secret_configured":true`) {
		t.Fatal("blank secret not retained")
	}
	w = serveMutatingAdmin(app, "PATCH", path, `{"enabled":true,"client_id":"another-app","allowed_user_ids":["42"]}`, csrf, cookie)
	if w.Code != 400 {
		t.Fatal("secret silently rebound")
	}
	w = serveMutatingAdmin(app, "PATCH", path, `{"enabled":true,"client_id":"test-app","allowed_user_ids":["username"]}`, csrf, cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatal("username accepted instead of numeric ID")
	}
	var detail string
	if err := st.DB().QueryRow(`SELECT detail_redacted FROM audit_events WHERE action='oauth.github_operator.update' LIMIT 1`).Scan(&detail); err != nil || strings.Contains(detail, "test-secret-value") {
		t.Fatal("audit not redacted", err)
	}
}
