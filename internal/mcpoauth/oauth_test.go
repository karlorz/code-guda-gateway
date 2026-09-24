package mcpoauth_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/mcpoauth"
	"code-guda-gateway/internal/secrets"
	"code-guda-gateway/internal/server"
	"code-guda-gateway/internal/store"
)

func setupTestServer(t *testing.T, cfg config.Config) (http.Handler, *sql.DB, *gatewaykeys.Service) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_oauth.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mkPath := filepath.Join(t.TempDir(), "master.key")
	mk, err := secrets.LoadOrCreate(mkPath)
	if err != nil {
		t.Fatalf("secrets.LoadOrCreate: %v", err)
	}

	gk := gatewaykeys.NewService(st.DB())
	srv := server.New(cfg, gk, st.DB(), mk)
	return srv, st.DB(), gk
}

func computePKCE() (verifier, challenge string) {
	verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge
}

func TestMetadataDocuments(t *testing.T) {
	issuer := "http://127.0.0.1:8080"
	pwd := "operator-secret-123"
	hash, err := mcpoauth.HashPassword(pwd)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	srv, _, _ := setupTestServer(t, config.Config{
		OAuthIssuer:       issuer,
		OAuthPasswordHash: hash,
	})

	// Test GET /.well-known/oauth-protected-resource
	paths := []string{
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
	}
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", p, rec.Code)
		}
		var meta map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
			t.Fatalf("%s decode json: %v", p, err)
		}
		if meta["resource"] != issuer+"/mcp" {
			t.Fatalf("%s resource = %v, want %s/mcp", p, meta["resource"], issuer)
		}
		authServers, ok := meta["authorization_servers"].([]any)
		if !ok || len(authServers) != 1 || authServers[0] != issuer {
			t.Fatalf("%s authorization_servers = %v, want [%s]", p, meta["authorization_servers"], issuer)
		}
	}

	// Test GET /.well-known/oauth-authorization-server
	{
		req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("/.well-known/oauth-authorization-server status = %d, want 200", rec.Code)
		}
		var meta map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
			t.Fatalf("decode meta json: %v", err)
		}
		if meta["issuer"] != issuer {
			t.Fatalf("issuer = %v, want %s", meta["issuer"], issuer)
		}
		if meta["authorization_endpoint"] != issuer+"/authorize" {
			t.Fatalf("authorization_endpoint = %v", meta["authorization_endpoint"])
		}
		if meta["token_endpoint"] != issuer+"/token" {
			t.Fatalf("token_endpoint = %v", meta["token_endpoint"])
		}
		if meta["registration_endpoint"] != issuer+"/register" {
			t.Fatalf("registration_endpoint = %v", meta["registration_endpoint"])
		}
	}
}

func TestAbsentOrMalformedHash404(t *testing.T) {
	validIssuer := "http://127.0.0.1:8080"
	validHash, _ := mcpoauth.HashPassword("valid-password")

	tests := []struct {
		name   string
		issuer string
		hash   string
	}{
		{"absent both", "", ""},
		{"absent hash", validIssuer, ""},
		{"absent issuer", "", validHash},
		{"malformed hash prefix", validIssuer, "bcrypt$123"},
		{"malformed hash parts", validIssuer, "scrypt$16384$8$1$salt"},
		{"malformed issuer path", "http://127.0.0.1:8080/somepath", validHash},
		{"malformed issuer query", "http://127.0.0.1:8080?foo=bar", validHash},
		{"non-loopback http issuer", "http://example.com", validHash},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _, _ := setupTestServer(t, config.Config{
				OAuthIssuer:       tt.issuer,
				OAuthPasswordHash: tt.hash,
			})

			for _, path := range []string{
				"/.well-known/oauth-protected-resource",
				"/.well-known/oauth-authorization-server",
				"/register",
				"/authorize",
				"/token",
			} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				if rec.Code != http.StatusNotFound {
					t.Fatalf("expected 404 for %s when %s, got %d", path, tt.name, rec.Code)
				}
			}
		})
	}
}

func TestClientRegistrationAndRedirectValidation(t *testing.T) {
	issuer := "http://localhost:8080"
	pwd := "operator-pass"
	hash, _ := mcpoauth.HashPassword(pwd)

	srv, _, _ := setupTestServer(t, config.Config{
		OAuthIssuer:       issuer,
		OAuthPasswordHash: hash,
	})

	// Non-https and non-loopback http rejected
	{
		body := `{"client_name":"Bad","redirect_uris":["http://attacker.com/cb"]}`
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad redirect status = %d, want 400", rec.Code)
		}
	}

	// Valid registration with https and loopback http
	{
		body := `{"client_name":"Doubao","redirect_uris":["https://doubao.com/callback","http://127.0.0.1:9999/cb"]}`
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("register status = %d, want 201: %s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp["client_id"] == "" || resp["client_name"] != "Doubao" {
			t.Fatalf("invalid resp: %v", resp)
		}
	}
}

func TestConsentPageRendering(t *testing.T) {
	issuer := "http://localhost:8080"
	pwd := "secret123"
	hash, _ := mcpoauth.HashPassword(pwd)

	srv, _, _ := setupTestServer(t, config.Config{
		OAuthIssuer:       issuer,
		OAuthPasswordHash: hash,
	})

	// Register client with name Doubao
	regBody := `{"client_name":"Doubao","redirect_uris":["https://example.com/oauth/callback"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(regBody))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var regResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	clientID := regResp["client_id"].(string)

	_, challenge := computePKCE()

	// GET /authorize with Doubao
	{
		authURL := fmt.Sprintf("/authorize?client_id=%s&redirect_uri=%s&response_type=code&code_challenge=%s&code_challenge_method=S256&state=xyz123",
			clientID, url.QueryEscape("https://example.com/oauth/callback"), challenge)
		req := httptest.NewRequest(http.MethodGet, authURL, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Doubao") {
			t.Fatalf("expected Doubao in body, got: %s", body)
		}
		if strings.Contains(strings.ToLower(body), "chatgpt") {
			t.Fatalf("body contains hardcoded chatgpt: %s", body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("cache-control = %q, want no-store", rec.Header().Get("Cache-Control"))
		}
	}

	// Register client with name containing '<' and verify escaping
	{
		maliciousName := `<script>alert("xss")</script>`
		regBody := fmt.Sprintf(`{"client_name":%q,"redirect_uris":["https://example.com/oauth/callback"]}`, maliciousName)
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(regBody))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		var xssResp map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &xssResp)
		xssClientID := xssResp["client_id"].(string)

		authURL := fmt.Sprintf("/authorize?client_id=%s&redirect_uri=%s&response_type=code&code_challenge=%s&code_challenge_method=S256&state=stateX",
			xssClientID, url.QueryEscape("https://example.com/oauth/callback"), challenge)
		req = httptest.NewRequest(http.MethodGet, authURL, nil)
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		body := rec.Body.String()
		if strings.Contains(body, "<script>") {
			t.Fatal("HTML injection vulnerability: unescaped <script> tag found")
		}
		if !strings.Contains(body, "&lt;script&gt;") {
			t.Fatal("HTML should contain escaped &lt;script&gt;")
		}
	}

	// Client with empty name -> displays "this client"
	{
		regBody := `{"client_name":"","redirect_uris":["https://example.com/oauth/callback"]}`
		req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(regBody))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		var emptyResp map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &emptyResp)
		emptyClientID := emptyResp["client_id"].(string)

		authURL := fmt.Sprintf("/authorize?client_id=%s&redirect_uri=%s&response_type=code&code_challenge=%s&code_challenge_method=S256&state=stateE",
			emptyClientID, url.QueryEscape("https://example.com/oauth/callback"), challenge)
		req = httptest.NewRequest(http.MethodGet, authURL, nil)
		rec = httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		body := rec.Body.String()
		if !strings.Contains(body, "This operator password is set at /admin after admin sign-in and is not the admin token.") {
			t.Fatalf("expected consent form guidance sentence, got: %s", body)
		}
		if !strings.Contains(body, "this client") {
			t.Fatalf("expected 'this client' for empty name, got: %s", body)
		}
	}

	// Reject un-registered redirect_uri
	{
		authURL := fmt.Sprintf("/authorize?client_id=%s&redirect_uri=%s&response_type=code&code_challenge=%s&code_challenge_method=S256",
			clientID, url.QueryEscape("https://unregistered.com/callback"), challenge)
		req := httptest.NewRequest(http.MethodGet, authURL, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("unregistered redirect_uri status = %d, want 400", rec.Code)
		}
	}
}

func TestAuthorizePostWrongPasswordAndSuccess(t *testing.T) {
	issuer := "http://localhost:8080"
	pwd := "correct-operator-password"
	hash, _ := mcpoauth.HashPassword(pwd)

	srv, db, _ := setupTestServer(t, config.Config{
		OAuthIssuer:       issuer,
		OAuthPasswordHash: hash,
	})

	regBody := `{"client_name":"ClaudeBot","redirect_uris":["https://example.com/callback"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(regBody))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var regResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	clientID := regResp["client_id"].(string)

	_, challenge := computePKCE()

	// POST /authorize wrong password -> 401 re-renders form
	{
		form := url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {"https://example.com/callback"},
			"state":                 {"random-state-1"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"password":              {"wrong-password"},
		}
		req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password status = %d, want 401", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Invalid operator password") {
			t.Fatalf("re-rendered form does not show error message: %s", rec.Body.String())
		}
	}

	// POST /authorize with no settings row -> env password works
	{
		form := url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {"https://example.com/callback"},
			"state":                 {"random-state-1"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"password":              {pwd},
		}
		req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Fatalf("correct env password status = %d, want 302", rec.Code)
		}
		loc := rec.Header().Get("Location")
		u, err := url.Parse(loc)
		if err != nil {
			t.Fatalf("parse location: %v", err)
		}
		if u.Scheme != "https" || u.Host != "example.com" || u.Path != "/callback" {
			t.Fatalf("wrong location: %s", loc)
		}
		if u.Query().Get("state") != "random-state-1" {
			t.Fatalf("wrong state: %s", u.Query().Get("state"))
		}
		if u.Query().Get("code") == "" {
			t.Fatal("missing code in redirect URL")
		}
	}

	// Dynamic override in settings:
	// After saving a new password hash to settings row, POST /authorize accepts new password and rejects previous env password
	newPwd := "new-dynamic-operator-password"
	newHash, err := mcpoauth.HashPassword(newPwd)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('oauth_operator_password_hash', ?, ?)`, newHash, now); err != nil {
		t.Fatalf("insert settings row: %v", err)
	}

	// Old env password is now rejected
	{
		form := url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {"https://example.com/callback"},
			"state":                 {"random-state-2"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"password":              {pwd},
		}
		req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("old env password after rotate status = %d, want 401", rec.Code)
		}
	}

	// New password is accepted without restart
	{
		form := url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {"https://example.com/callback"},
			"state":                 {"random-state-2"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"password":              {newPwd},
		}
		req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("new password status = %d, want 302", rec.Code)
		}
	}

	// Malformed settings value falls back to env password
	if _, err := db.Exec(`UPDATE settings SET value = 'not-a-valid-scrypt-hash' WHERE key = 'oauth_operator_password_hash'`); err != nil {
		t.Fatalf("update settings row malformed: %v", err)
	}
	{
		form := url.Values{
			"client_id":             {clientID},
			"redirect_uri":          {"https://example.com/callback"},
			"state":                 {"random-state-3"},
			"code_challenge":        {challenge},
			"code_challenge_method": {"S256"},
			"password":              {pwd},
		}
		req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound {
			t.Fatalf("env password on malformed settings status = %d, want 302", rec.Code)
		}
	}
}

func TestPKCESuccessReplayAndWrongVerifier(t *testing.T) {
	issuer := "http://localhost:8080"
	pwd := "testpass"
	hash, _ := mcpoauth.HashPassword(pwd)

	srv, _, gk := setupTestServer(t, config.Config{
		OAuthIssuer:       issuer,
		OAuthPasswordHash: hash,
	})

	regBody := `{"client_name":"MyClient","redirect_uris":["https://example.com/cb"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(regBody))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var regResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	clientID := regResp["client_id"].(string)

	verifier, challenge := computePKCE()

	// Get code
	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://example.com/cb"},
		"state":                 {"st"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"password":              {pwd},
	}
	req = httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	locURL, _ := url.Parse(rec.Header().Get("Location"))
	code := locURL.Query().Get("code")

	// Token exchange with wrong verifier -> 400 invalid_grant
	{
		tokForm := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {"https://example.com/cb"},
			"client_id":     {clientID},
			"code_verifier": {"wrong-verifier-value"},
		}
		req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokForm.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("wrong verifier status = %d, want 400", rec.Code)
		}
		// Code was consumed!
	}

	// Code replay -> fails with 400 invalid_grant
	{
		tokForm := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {"https://example.com/cb"},
			"client_id":     {clientID},
			"code_verifier": {verifier},
		}
		req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokForm.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("code replay status = %d, want 400", rec.Code)
		}
	}

	// Generate fresh code for successful exchange
	req = httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	locURL, _ = url.Parse(rec.Header().Get("Location"))
	code = locURL.Query().Get("code")

	// Exchange successfully
	var tokenResp map[string]any
	{
		tokForm := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {"https://example.com/cb"},
			"client_id":     {clientID},
			"code_verifier": {verifier},
		}
		req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokForm.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("token exchange status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &tokenResp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
	}

	accessToken := tokenResp["access_token"].(string)
	if !strings.HasPrefix(accessToken, "gsk_") {
		t.Fatalf("access token format = %s, want gsk_ prefix", accessToken)
	}

	// Verify key via gatewaykeys.Verify
	key, err := gk.Verify(accessToken)
	if err != nil {
		t.Fatalf("gk.Verify: %v", err)
	}
	if key == nil || key.Name != "oauth:MyClient" {
		t.Fatalf("key name = %v, want oauth:MyClient", key)
	}
}

func TestAccessExpiryAndRefresh(t *testing.T) {
	issuer := "http://localhost:8080"
	pwd := "testpass"
	hash, _ := mcpoauth.HashPassword(pwd)

	srv, db, gk := setupTestServer(t, config.Config{
		OAuthIssuer:       issuer,
		OAuthPasswordHash: hash,
		InternalToken:     "internal-secret-token",
	})

	// Register client
	regBody := `{"client_name":"ExpiryClient","redirect_uris":["https://example.com/cb"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(regBody))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var regResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	clientID := regResp["client_id"].(string)

	verifier, challenge := computePKCE()

	// Authorize and get token
	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://example.com/cb"},
		"state":                 {"st"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"password":              {pwd},
	}
	req = httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	locURL, _ := url.Parse(rec.Header().Get("Location"))
	code := locURL.Query().Get("code")

	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://example.com/cb"},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	req = httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var tokenResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tokenResp)
	accessToken := tokenResp["access_token"].(string)
	refreshToken := tokenResp["refresh_token"].(string)

	// 1. Verify before expiry via /internal/keys/verify -> 200
	{
		body := `{"token":"` + accessToken + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", "internal-secret-token")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("internal verify status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	}

	// 2. Fast forward access_expires_at to the past
	past := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE oauth_grants SET access_expires_at = ?`, past); err != nil {
		t.Fatalf("update access_expires_at: %v", err)
	}

	// 3. /internal/keys/verify should now return 401 even though key is still enabled
	{
		// confirm key is enabled in DB
		key, err := gk.Verify(accessToken)
		if err != nil || key == nil || !key.Enabled {
			t.Fatalf("key should still be valid at gatewaykeys layer: key=%v, err=%v", key, err)
		}

		body := `{"token":"` + accessToken + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", "internal-secret-token")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("internal verify after expiry status = %d, want 401", rec.Code)
		}
	}

	// 4. Refresh token should work even after access expiry
	var successorAccess, successorRefresh string
	{
		refForm := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {refreshToken},
			"client_id":     {clientID},
		}
		req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(refForm.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("refresh token status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var refResp map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &refResp)
		successorAccess = refResp["access_token"].(string)
		successorRefresh = refResp["refresh_token"].(string)

		if successorAccess == accessToken {
			t.Fatal("successor access token should be different from old access token")
		}
		if successorRefresh == refreshToken {
			t.Fatal("successor refresh token should be different from old refresh token")
		}
	}

	// 5. Old key is disabled
	{
		key, err := gk.Verify(accessToken)
		if err != gatewaykeys.ErrNotAuthorized {
			t.Fatalf("old key verify error = %v, want ErrNotAuthorized; key=%v", err, key)
		}
	}

	// 6. Successor key verifies on /internal/keys/verify -> 200
	{
		body := `{"token":"` + successorAccess + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", "internal-secret-token")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("successor key verify status = %d, want 200", rec.Code)
		}
	}

	// 7. Expired refresh token (14d) fails refresh
	past14d := time.Now().UTC().Add(-1 * time.Minute).Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE oauth_grants SET refresh_expires_at = ?`, past14d); err != nil {
		t.Fatalf("update refresh_expires_at: %v", err)
	}
	{
		refForm := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {successorRefresh},
			"client_id":     {clientID},
		}
		req := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(refForm.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expired refresh token status = %d, want 400", rec.Code)
		}
	}
}

func TestConcurrentRefresh(t *testing.T) {
	issuer := "http://localhost:8080"
	pwd := "testpass"
	hash, _ := mcpoauth.HashPassword(pwd)

	srv, _, _ := setupTestServer(t, config.Config{
		OAuthIssuer:       issuer,
		OAuthPasswordHash: hash,
	})

	regBody := `{"client_name":"ConcurrentClient","redirect_uris":["https://example.com/cb"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(regBody))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var regResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	clientID := regResp["client_id"].(string)

	verifier, challenge := computePKCE()

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://example.com/cb"},
		"state":                 {"st"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"password":              {pwd},
	}
	req = httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	locURL, _ := url.Parse(rec.Header().Get("Location"))
	code := locURL.Query().Get("code")

	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://example.com/cb"},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	req = httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var tokenResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tokenResp)
	refreshToken := tokenResp["refresh_token"].(string)

	const concurrency = 8
	var wg sync.WaitGroup
	wg.Add(concurrency)

	statusCodes := make([]int, concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			f := url.Values{
				"grant_type":    {"refresh_token"},
				"refresh_token": {refreshToken},
				"client_id":     {clientID},
			}
			r := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(f.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, r)
			statusCodes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	successCount := 0
	badRequestCount := 0
	for _, code := range statusCodes {
		if code == http.StatusOK {
			successCount++
		} else if code == http.StatusBadRequest {
			badRequestCount++
		}
	}

	if successCount != 1 {
		t.Fatalf("expected exactly 1 success out of concurrent refreshes, got %d (all statuses: %v)", successCount, statusCodes)
	}
	if badRequestCount != concurrency-1 {
		t.Fatalf("expected %d bad requests, got %d", concurrency-1, badRequestCount)
	}
}

func TestRevokedKeyCannotRefresh(t *testing.T) {
	issuer := "http://localhost:8080"
	pwd := "testpass"
	hash, _ := mcpoauth.HashPassword(pwd)

	srv, db, gk := setupTestServer(t, config.Config{
		OAuthIssuer:       issuer,
		OAuthPasswordHash: hash,
	})

	regBody := `{"client_name":"RevokeClient","redirect_uris":["https://example.com/cb"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(regBody))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var regResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	clientID := regResp["client_id"].(string)

	verifier, challenge := computePKCE()

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://example.com/cb"},
		"state":                 {"st"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"password":              {pwd},
	}
	req = httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	locURL, _ := url.Parse(rec.Header().Get("Location"))
	code := locURL.Query().Get("code")

	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://example.com/cb"},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	req = httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var tokenResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tokenResp)
	accessToken := tokenResp["access_token"].(string)
	refreshToken := tokenResp["refresh_token"].(string)

	// Revoke the key
	key, err := gk.Verify(accessToken)
	if err != nil || key == nil {
		t.Fatalf("gk.Verify before revoke: %v", err)
	}
	if err := gk.Revoke(key.ID); err != nil {
		t.Fatalf("gk.Revoke: %v", err)
	}

	// Try to refresh -> 400 invalid_grant
	refForm := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	req = httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(refForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("refresh on revoked key status = %d, want 400", rec.Code)
	}
	_ = db
}

func TestProxyRouteOAuthKeyRejectionAndManualKeySuccess(t *testing.T) {
	issuer := "http://localhost:8080"
	pwd := "testpass"
	hash, _ := mcpoauth.HashPassword(pwd)

	srv, _, gk := setupTestServer(t, config.Config{
		OAuthIssuer:       issuer,
		OAuthPasswordHash: hash,
		InternalToken:     "internal-token",
	})

	// 1. Manual key
	manualRawKey, _, err := gk.Create("manual-operator-key")
	if err != nil {
		t.Fatalf("create manual key: %v", err)
	}

	// 2. OAuth key
	regBody := `{"client_name":"OAuthProxyClient","redirect_uris":["https://example.com/cb"]}`
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(regBody))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var regResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	clientID := regResp["client_id"].(string)

	verifier, challenge := computePKCE()

	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://example.com/cb"},
		"state":                 {"st"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"password":              {pwd},
	}
	req = httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	locURL, _ := url.Parse(rec.Header().Get("Location"))
	code := locURL.Query().Get("code")

	tokForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://example.com/cb"},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	req = httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var tokenResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &tokenResp)
	oauthRawKey := tokenResp["access_token"].(string)

	// Both keys verify on /internal/keys/verify
	for _, k := range []string{manualRawKey, oauthRawKey} {
		body := `{"token":"` + k + `"}`
		req := httptest.NewRequest(http.MethodPost, "/internal/keys/verify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Token", "internal-token")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("key %s verify status = %d, want 200", k, rec.Code)
		}
	}

	// Test proxy route /grok/v1/models:
	// With OAuth key -> authorized() rejects -> 401 Unauthorized
	{
		req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+oauthRawKey)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("oauth key on proxy route status = %d, want 401 unauthorized", rec.Code)
		}
	}

	// With Manual key -> authorized() accepts (even if upstream fails / 502 / 500 / 404, it is NOT 401 unauthorized)
	{
		req := httptest.NewRequest(http.MethodGet, "/grok/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+manualRawKey)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("manual key on proxy route returned 401 unauthorized: should have passed gateway authorization")
		}
	}
}
