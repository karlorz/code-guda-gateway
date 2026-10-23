package operatorlogin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/store"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(st.DB(), []byte(strings.Repeat("k", 32)), "https://gateway.test")
	if err = s.Save(Update{Enabled: true, ClientID: "app123", ClientSecret: "test-app-secret", AllowedIDs: []string{"42"}}); err != nil {
		t.Fatal(err)
	}
	return s
}
func original() url.Values {
	return url.Values{"client_id": {"mcp-client"}, "redirect_uri": {"https://client.test/callback"}, "state": {"mcp-state"}, "scope": {"mcp offline_access"}, "resource": {"https://gateway.test/mcp"}, "code_challenge": {"mcp-challenge"}, "code_challenge_method": {"S256"}, "agent_label": {"test client"}}
}
func start(t *testing.T, s *Service) (*http.Cookie, string, url.Values) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "https://gateway.test/authorize", nil)
	r.Header.Set("Origin", s.issuer)
	if err := s.Start(w, r, original()); err != nil {
		t.Fatal(err)
	}
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	u, _ := url.Parse(w.Header().Get("Location"))
	q := u.Query()
	if u.Scheme != "https" || u.Host != "github.com" || q.Get("redirect_uri") != s.issuer+CallbackPath || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "" {
		t.Fatal("invalid GitHub authorization request")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe binding cookie")
	}
	return cookies[0], q.Get("state"), q
}
func callback(s *Service, cookie *http.Cookie, state string) *http.Request {
	r := httptest.NewRequest("GET", s.issuer+CallbackPath+"?code=dummy-code&state="+url.QueryEscape(state), nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}
func mockGitHub(t *testing.T, s *Service, id int64, q url.Values) *atomic.Int64 {
	t.Helper()
	calls := &atomic.Int64{}
	s.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		out := ""
		switch r.URL.String() {
		case "https://github.com/login/oauth/access_token":
			if r.Method != "POST" || r.Header.Get("Authorization") != "" {
				t.Error("wrong token request")
			}
			raw, _ := io.ReadAll(r.Body)
			form, _ := url.ParseQuery(string(raw))
			sum := sha256.Sum256([]byte(form.Get("code_verifier")))
			if q != nil && base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
				t.Error("PKCE not bound")
			}
			if form.Get("client_secret") != "test-app-secret" || form.Get("redirect_uri") != s.issuer+CallbackPath {
				t.Error("wrong secret/callback")
			}
			out = `{"access_token":"test-ephemeral-token","token_type":"bearer"}`
		case "https://api.github.com/user":
			if r.Header.Get("Authorization") != "Bearer test-ephemeral-token" {
				t.Error("missing identity token")
			}
			b, _ := json.Marshal(map[string]any{"id": id, "login": "operator-name"})
			out = string(b)
		default:
			t.Errorf("unexpected destination %s", r.URL)
			out = `{}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(out)), Request: r}, nil
	})
	return calls
}
func TestGitHubFlowBoundOneUseAndNoTokensStored(t *testing.T) {
	s := fixture(t)
	cookie, state, q := start(t, s)
	calls := mockGitHub(t, s, 42, q)
	form, proof, login, err := s.Finish(callback(s, cookie, state))
	if err != nil || login != "operator-name" || form.Encode() != original().Encode() {
		t.Fatalf("finish: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatal("wrong network count")
	}
	if _, _, _, err = s.Finish(callback(s, cookie, state)); err == nil {
		t.Fatal("state replay accepted")
	}
	r := httptest.NewRequest("POST", s.issuer+"/authorize", nil)
	r.AddCookie(cookie)
	r.Header.Set("Origin", s.issuer)
	if err = s.Consume(r, proof, form); err != nil {
		t.Fatal(err)
	}
	if err = s.Consume(r, proof, form); err == nil {
		t.Fatal("consent replay accepted")
	}
	rows, err := s.db.Query(`SELECT value FROM settings UNION ALL SELECT payload FROM operator_login_flows`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		rows.Scan(&v)
		if strings.Contains(v, "test-ephemeral-token") || strings.Contains(v, "test-app-secret") {
			t.Fatal("plaintext secret persisted")
		}
	}
}
func TestGitHubRejectsMissingWrongExpiredOrDeniedCallback(t *testing.T) {
	for _, kind := range []string{"no-cookie", "wrong-cookie", "state", "expired", "denied", "account", "disabled", "rotation", "duplicate-state", "duplicate-code"} {
		t.Run(kind, func(t *testing.T) {
			s := fixture(t)
			cookie, state, q := start(t, s)
			id := int64(42)
			if kind == "account" {
				id = 43
			}
			calls := mockGitHub(t, s, id, q)
			r := callback(s, cookie, state)
			switch kind {
			case "no-cookie":
				r = callback(s, nil, state)
			case "wrong-cookie":
				r = callback(s, &http.Cookie{Name: CookieName, Value: strings.Repeat("x", 43)}, state)
			case "state":
				r = callback(s, cookie, strings.Repeat("x", 43))
			case "expired":
				s.db.Exec(`UPDATE operator_login_flows SET expires_at=0`)
			case "denied":
				r.URL.RawQuery += "&error=access_denied"
			case "duplicate-state":
				r.URL.RawQuery += "&state=other"
			case "duplicate-code":
				r.URL.RawQuery += "&code=other"
			case "disabled":
				if err := s.Save(Update{Enabled: false, ClientID: "app123", AllowedIDs: []string{"42"}}); err != nil {
					t.Fatal(err)
				}
			case "rotation":
				if err := s.Save(Update{Enabled: true, ClientID: "app123", AllowedIDs: []string{"42"}}); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, _, err := s.Finish(r); err == nil {
				t.Fatal("invalid callback accepted")
			}
			if kind != "account" && calls.Load() != 0 {
				t.Fatal("invalid callback contacted GitHub")
			}
		})
	}
}
func TestConsentRejectsTamperingAndWrongBrowser(t *testing.T) {
	for _, kind := range []string{"scope", "resource", "client_id", "redirect_uri", "state", "code_challenge", "agent_label", "cookie", "origin", "expired", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			s := fixture(t)
			cookie, state, q := start(t, s)
			mockGitHub(t, s, 42, q)
			form, proof, _, err := s.Finish(callback(s, cookie, state))
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", s.issuer+"/authorize", nil)
			r.Header.Set("Origin", s.issuer)
			switch kind {
			case "cookie":
				cookie = &http.Cookie{Name: CookieName, Value: strings.Repeat("x", 43)}
			case "origin":
				r.Header.Set("Origin", "https://attacker.test")
			case "expired":
				s.db.Exec(`UPDATE operator_login_flows SET expires_at=0`)
			case "disabled":
				s.Save(Update{Enabled: false, ClientID: "app123", AllowedIDs: []string{"42"}})
			default:
				form.Set(kind, "tampered")
			}
			r.AddCookie(cookie)
			if err = s.Consume(r, proof, form); err == nil {
				t.Fatal("invalid consent accepted")
			}
		})
	}
}
func TestSettingsSecretRetentionAndIdentityValidation(t *testing.T) {
	s := fixture(t)
	before, _ := s.load()
	status, _ := s.Status()
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "secret_ciphertext") || strings.Contains(string(raw), "test-app-secret") || !status.SecretConfigured {
		t.Fatal("secret exposed")
	}
	if err := s.Save(Update{Enabled: true, ClientID: "app123", AllowedIDs: []string{"42", "42", "7"}}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.load()
	if before.Secret != after.Secret || before.Revision == after.Revision || len(after.AllowedIDs) != 2 {
		t.Fatal("blank secret not preserved or revision unchanged")
	}
	for _, id := range []string{"username", "0", "-1", "042", "9223372036854775808"} {
		if s.Save(Update{Enabled: true, ClientID: "app123", AllowedIDs: []string{id}}) == nil {
			t.Fatalf("invalid ID accepted: %s", id)
		}
	}
	if s.Save(Update{Enabled: true, ClientID: "different-app", AllowedIDs: []string{"42"}}) == nil {
		t.Fatal("secret silently moved to another app")
	}
	if s.Save(Update{Enabled: true, ClientID: "app123", AllowedIDs: []string{}}) == nil {
		t.Fatal("empty allowlist enabled")
	}
	encrypted, _ := base64.RawStdEncoding.DecodeString(after.Secret)
	plain, err := secrets.Decrypt(s.key, encrypted)
	if err != nil || string(plain) != "test-app-secret" {
		t.Fatal("secret encryption failed")
	}
}
func TestConcurrentConsentOnlyOnce(t *testing.T) {
	s := fixture(t)
	cookie, state, q := start(t, s)
	mockGitHub(t, s, 42, q)
	form, proof, _, err := s.Finish(callback(s, cookie, state))
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("POST", s.issuer+"/authorize", nil)
			r.AddCookie(cookie)
			if s.Consume(r, proof, form) == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal("consent not atomic", successes.Load())
	}
}
func TestGitHubNetworkFailuresAndLimits(t *testing.T) {
	for _, kind := range []string{"redirect", "oversize", "invalid-json", "error-token", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			s := fixture(t)
			cookie, state, _ := start(t, s)
			s.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				status := 200
				body := "invalid"
				switch kind {
				case "redirect":
					status = 302
				case "oversize":
					body = strings.Repeat("x", 65537)
				case "error-token":
					body = `{"error":"bad_verification_code","access_token":"ignore","token_type":"bearer"}`
				case "timeout":
					return nil, context.DeadlineExceeded
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Location": []string{"https://attacker.test"}}, Request: r}, nil
			})
			if _, _, _, err := s.Finish(callback(s, cookie, state)); err == nil {
				t.Fatal("failed GitHub response accepted")
			}
		})
	}
}
func TestDisabledAndCrossSiteStart(t *testing.T) {
	s := fixture(t)
	for _, kind := range []string{"origin", "fetch-site", "disabled"} {
		r := httptest.NewRequest("POST", s.issuer+"/authorize", nil)
		switch kind {
		case "origin":
			r.Header.Set("Origin", "https://attacker.test")
		case "fetch-site":
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		case "disabled":
			s.Save(Update{ClientID: "app123", AllowedIDs: []string{"42"}})
		}
		if s.Start(httptest.NewRecorder(), r, original()) == nil {
			t.Fatal("invalid start accepted", kind)
		}
	}
}
func TestFlowExpiry(t *testing.T) {
	s := fixture(t)
	_, _, _ = start(t, s)
	var expiry int64
	if err := s.db.QueryRow(`SELECT expires_at FROM operator_login_flows`).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if delta := time.Until(time.Unix(expiry, 0)); delta < 9*time.Minute || delta > ttl {
		t.Fatal("unexpected expiry", delta)
	}
}
