package adminauth_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"code-guda-gateway/internal/adminauth"
	"code-guda-gateway/internal/store"
)

func openTestService(t *testing.T) (*adminauth.Service, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return adminauth.NewService(st.DB(), 24*time.Hour), st
}

func TestTokenInit_StoresHashReturnsRaw(t *testing.T) {
	t.Parallel()
	svc, st := openTestService(t)

	raw, err := svc.Init()
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !regexp.MustCompile(`^gat_[A-Za-z0-9]{32}$`).MatchString(raw) {
		t.Fatalf("raw token format: %q", raw)
	}

	var hash, prefix string
	err = st.DB().QueryRow(`SELECT token_hash, key_prefix FROM admin_tokens LIMIT 1`).Scan(&hash, &prefix)
	if err != nil {
		t.Fatalf("query admin_tokens: %v", err)
	}
	if hash == raw {
		t.Fatal("stored token_hash equals raw token")
	}
	if len(hash) != 64 {
		t.Fatalf("token_hash hex len = %d, want 64", len(hash))
	}
	if prefix != raw[:8] {
		t.Fatalf("key_prefix = %q, want %q", prefix, raw[:8])
	}

	var blob string
	rows, err := st.DB().Query(`SELECT token_hash, key_prefix FROM admin_tokens`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h, p string
		_ = rows.Scan(&h, &p)
		blob += h + p
	}
	if strings.Contains(blob, raw) {
		t.Fatal("raw token appears in admin_tokens columns")
	}
}

func TestTokenVerify_AcceptsRawRejectsBogus(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, err := svc.Init()
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	ok, err := svc.Verify(raw)
	if err != nil || !ok {
		t.Fatalf("Verify(raw): ok=%v err=%v", ok, err)
	}
	ok, err = svc.Verify("gat_bogus")
	if err != nil || ok {
		t.Fatalf("Verify(bogus): ok=%v err=%v", ok, err)
	}
	ok, err = svc.Verify("")
	if err != nil || ok {
		t.Fatalf("Verify(empty): ok=%v err=%v", ok, err)
	}
}

func TestTokenRotate_InvalidatesOld(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	oldRaw, err := svc.Init()
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	newRaw, err := svc.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if newRaw == oldRaw {
		t.Fatal("rotate returned same raw token")
	}
	ok, _ := svc.Verify(oldRaw)
	if ok {
		t.Fatal("old raw token still verifies after rotate")
	}
	ok, _ = svc.Verify(newRaw)
	if !ok {
		t.Fatal("new raw token does not verify")
	}
}

func TestToken_RawTokenOneTimeDisplay(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, err := svc.Init()
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	prefix, err := svc.CurrentPrefix()
	if err != nil {
		t.Fatalf("CurrentPrefix: %v", err)
	}
	if prefix == raw {
		t.Fatal("CurrentPrefix returned full raw token")
	}
	if prefix != raw[:8] {
		t.Fatalf("prefix %q != %q", prefix, raw[:8])
	}
}

func TestSessionLogin_ValidTokenCreatesSession(t *testing.T) {
	t.Parallel()
	svc, st := openTestService(t)
	raw, err := svc.Init()
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	res, err := svc.Login(raw)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if res.SessionID == "" {
		t.Fatal("empty session id")
	}
	var id string
	err = st.DB().QueryRow(`SELECT id FROM admin_sessions WHERE id = ?`, res.SessionID).Scan(&id)
	if err != nil {
		t.Fatalf("session row: %v", err)
	}
	c := res.Cookie
	if c.Name != adminauth.SessionCookieName {
		t.Fatalf("cookie name %q", c.Name)
	}
	if !c.HttpOnly {
		t.Fatal("cookie HttpOnly want true")
	}
	if !c.Secure {
		t.Fatal("cookie Secure want true")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/admin" {
		t.Fatalf("cookie Path = %q, want /admin", c.Path)
	}
}

func TestSessionLogin_InvalidTokenReturnsError(t *testing.T) {
	t.Parallel()
	svc, st := openTestService(t)
	if _, err := svc.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	res, err := svc.Login("gat_notavalidtokenatallxxxxxxxxxx")
	if err == nil || res != nil {
		t.Fatalf("Login bogus: res=%v err=%v", res, err)
	}
	var n int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM admin_sessions`).Scan(&n)
	if n != 0 {
		t.Fatalf("sessions count = %d, want 0", n)
	}
}

func TestSessionValidate_ValidSidTrue(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, _ := svc.Init()
	res, err := svc.Login(raw)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	ok, err := svc.ValidateSession(res.SessionID)
	if err != nil || !ok {
		t.Fatalf("ValidateSession: ok=%v err=%v", ok, err)
	}
}

func TestSessionValidate_PastExpiryFalse(t *testing.T) {
	t.Parallel()
	svc, st := openTestService(t)
	raw, _ := svc.Init()
	res, err := svc.Login(raw)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(`UPDATE admin_sessions SET expires_at = ? WHERE id = ?`, past, res.SessionID); err != nil {
		t.Fatalf("update expires_at: %v", err)
	}
	ok, err := svc.ValidateSession(res.SessionID)
	if err != nil || ok {
		t.Fatalf("ValidateSession past expiry: ok=%v err=%v", ok, err)
	}
}

func TestSessionValidate_ExpiredOrRevokedFalse(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, _ := svc.Init()
	res, _ := svc.Login(raw)
	if _, err := svc.Logout(res.SessionID); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	ok, err := svc.ValidateSession(res.SessionID)
	if err != nil || ok {
		t.Fatalf("ValidateSession after logout: ok=%v err=%v", ok, err)
	}
}

func TestSessionLogout_ClearsSession(t *testing.T) {
	t.Parallel()
	svc, st := openTestService(t)
	raw, _ := svc.Init()
	res, _ := svc.Login(raw)
	clearCookie, err := svc.Logout(res.SessionID)
	if err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if clearCookie.Name != adminauth.SessionCookieName {
		t.Fatalf("clear cookie name %q", clearCookie.Name)
	}
	if clearCookie.MaxAge != -1 {
		t.Fatalf("clear cookie MaxAge = %d, want -1", clearCookie.MaxAge)
	}
	var n int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM admin_sessions WHERE id = ?`, res.SessionID).Scan(&n)
	if n != 0 {
		t.Fatalf("session row still exists")
	}
	ok, _ := svc.ValidateSession(res.SessionID)
	if ok {
		t.Fatal("session still valid after logout")
	}
}

func TestMiddleware_WithoutCookieReturns401(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := svc.Middleware(adminauth.MiddlewareConfig{}, next)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMiddleware_WithValidCookiePassesThrough(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	raw, _ := svc.Init()
	login, _ := svc.Login(raw)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := svc.Middleware(adminauth.MiddlewareConfig{}, next)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	req.AddCookie(login.Cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMiddleware_WithInvalidCookieReturns401(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := svc.Middleware(adminauth.MiddlewareConfig{}, next)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/foo", nil)
	req.AddCookie(&http.Cookie{Name: adminauth.SessionCookieName, Value: "deadbeef"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestMiddleware_LoginRouteExcluded(t *testing.T) {
	t.Parallel()
	svc, _ := openTestService(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	cfg := adminauth.MiddlewareConfig{
		ExcludePaths: map[string]struct{}{"/admin/api/login": {}},
	}
	h := svc.Middleware(cfg, next)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login path status = %d, want 200", rec.Code)
	}
}