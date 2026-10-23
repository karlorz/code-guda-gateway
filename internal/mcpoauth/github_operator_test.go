package mcpoauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-guda-gateway/internal/operatorlogin"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/store"
)

func githubConsentServer(t *testing.T) (*Server, []byte, url.Values) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "oauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, err := HashPassword("existing-password")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer("https://gateway.test", hash, st.DB())
	key := []byte(strings.Repeat("k", 32))
	s.GitHub = operatorlogin.New(st.DB(), key, s.issuer)
	if err = s.GitHub.Save(operatorlogin.Update{Enabled: true, ClientID: "github-app", ClientSecret: "github-app-secret", AllowedIDs: []string{"42"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO oauth_clients(client_id,client_name,redirect_uris,created_at) VALUES('client','MCP test','["https://client.test/callback"]',?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return s, key, consentValues("client", "https://client.test/callback", "client-state", "challenge", "S256", "mcp offline_access config:write", s.issuer+"/mcp", "MCP test", "prefilled-invite")
}
func seedConsent(t *testing.T, s *Server, key []byte, form url.Values) (string, *http.Cookie) {
	t.Helper()
	proof := strings.Repeat("p", 43)
	browser := strings.Repeat("b", 43)
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key='oauth_github_operator'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Revision string `json:"revision"`
	}
	json.Unmarshal([]byte(raw), &cfg)
	payload, _ := json.Marshal(map[string]any{"form": form, "user_id": "42", "login": "allowed-operator"})
	sealed, err := secrets.Encrypt(key, payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
	_, err = s.db.Exec(`INSERT INTO operator_login_flows VALUES(?,?,?,?,?,?)`, digest(proof), digest(browser), cfg.Revision, "consent", base64.RawStdEncoding.EncodeToString(sealed), time.Now().Add(time.Minute).Unix())
	if err != nil {
		t.Fatal(err)
	}
	return proof, &http.Cookie{Name: operatorlogin.CookieName, Value: browser}
}
func submit(s *Server, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", s.issuer+"/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", s.issuer)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}
func TestGitHubOperatorConsentIssuesBoundGrantOnlyOnce(t *testing.T) {
	s, key, form := githubConsentServer(t)
	proof, cookie := seedConsent(t, s, key, form)
	form.Set("github_consent", proof)
	w := submit(s, form, cookie)
	if w.Code != 302 {
		t.Fatalf("consent %d %s", w.Code, w.Body.String())
	}
	u, _ := url.Parse(w.Header().Get("Location"))
	if u.Host != "client.test" || u.Query().Get("state") != "client-state" || u.Query().Get("code") == "" {
		t.Fatal("client callback lost")
	}
	var scope, resource, via string
	if err := s.db.QueryRow(`SELECT scope,resource,issued_via FROM oauth_codes`).Scan(&scope, &resource, &via); err != nil {
		t.Fatal(err)
	}
	if scope != "mcp offline_access config:write" || resource != s.issuer+"/mcp" || via != "operator" {
		t.Fatal("grant binding/authority changed")
	}
	if w = submit(s, form, cookie); w.Code != 403 {
		t.Fatal("replayed consent accepted", w.Code)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM oauth_codes`).Scan(&n)
	if n != 1 {
		t.Fatal("extra code issued")
	}
}
func TestGitHubProofCannotChangePermissions(t *testing.T) {
	s, key, form := githubConsentServer(t)
	form.Set("scope", "mcp offline_access")
	proof, cookie := seedConsent(t, s, key, form)
	form.Set("github_consent", proof)
	form.Set("scope", "mcp offline_access config:write")
	w := submit(s, form, cookie)
	if w.Code != 403 {
		t.Fatal("scope tampering accepted", w.Code)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM oauth_codes`).Scan(&n)
	if n != 0 {
		t.Fatal("tampering issued code")
	}
}
func TestGitHubLoginIsSeparateFromConsentAndPasswordStillWorks(t *testing.T) {
	s, _, form := githubConsentServer(t)
	form.Set("operator_login", "github")
	w := submit(s, form, nil)
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "https://github.com/login/oauth/authorize?") {
		t.Fatal("GitHub sign-in not started", w.Code, w.Body.String())
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM oauth_codes`).Scan(&n)
	if n != 0 {
		t.Fatal("login auto approved")
	}
	form.Del("operator_login")
	form.Set("password", "existing-password")
	if w = submit(s, form, nil); w.Code != 302 {
		t.Fatal("password broken", w.Code, w.Body.String())
	}
}
func TestGitHubConsentPagePreservesInviteAndEscapesIdentity(t *testing.T) {
	s, _, form := githubConsentServer(t)
	r := httptest.NewRequest("GET", s.issuer+"/authorize?"+form.Encode(), nil)
	w := httptest.NewRecorder()
	s.handleAuthorizeGet(w, r, "test-proof", "<script>bad</script>")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `name="github_consent" value="test-proof"`) || !strings.Contains(body, `name="invite" value="prefilled-invite"`) || strings.Contains(body, "<script>") || strings.Contains(body, `id="password"`) {
		t.Fatal("bad confirmation form", body)
	}
	if w.Header().Get("Referrer-Policy") != "same-origin" {
		t.Fatal("same-origin form submission loses Origin")
	}
	if strings.Contains(w.Header().Get("Content-Security-Policy"), "form-action 'self'") {
		t.Fatal("CSP blocks OAuth redirect")
	}
}
