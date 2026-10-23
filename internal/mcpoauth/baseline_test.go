package mcpoauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/mcpoauth"
)

func TestAuthorizationBaselineResourceScopesAndRefreshReuse(t *testing.T) {
	const issuer = "https://search.example"
	const password = "test-operator-password"
	hash, _ := mcpoauth.HashPassword(password)
	srv, db, _ := setupTestServer(t, config.Config{OAuthIssuer: issuer, OAuthPasswordHash: hash, InternalToken: "test-internal"})
	call := func(path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w
	}
	r := httptest.NewRequest("POST", "/register", strings.NewReader(`{"client_name":"Baseline","redirect_uris":["https://client.example/cb"]}`))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	// Registration also returns an array, so decode only the identity field.
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reg); err != nil || reg.ClientID == "" {
		t.Fatal("registration failed")
	}
	verifier, challenge := computePKCE()
	auth := url.Values{"client_id": {reg.ClientID}, "redirect_uri": {"https://client.example/cb"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "password": {password}, "scope": {"mcp offline_access config:write"}, "resource": {issuer + "/mcp"}}
	wrong := auth.Get("resource")
	auth.Set("resource", "https://other.example/mcp")
	if got := call("/authorize", auth).Code; got != 400 {
		t.Fatalf("wrong-resource authorization: %d", got)
	}
	auth.Set("resource", wrong)
	w = call("/authorize", auth)
	if w.Code != 302 {
		t.Fatalf("authorize: %d", w.Code)
	}
	location, _ := url.Parse(w.Header().Get("Location"))
	grant := url.Values{"grant_type": {"authorization_code"}, "client_id": {reg.ClientID}, "redirect_uri": {"https://client.example/cb"}, "code_verifier": {verifier}, "code": {location.Query().Get("code")}, "resource": {issuer + "/mcp"}}
	w = call("/token", grant)
	if w.Code != 200 {
		t.Fatalf("exchange: %d", w.Code)
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil || token.Access == "" || token.Refresh == "" {
		t.Fatal("invalid exchange response")
	}
	if token.Scope != "mcp offline_access config:write" {
		t.Fatalf("granted scope: %s", token.Scope)
	}
	verify := func(access, resource string) int {
		body, _ := json.Marshal(map[string]string{"token": access, "resource": resource})
		r := httptest.NewRequest("POST", "/internal/keys/verify", strings.NewReader(string(body)))
		r.Header.Set("X-Internal-Token", "test-internal")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Code
	}
	if verify(token.Access, issuer+"/mcp") != 200 || verify(token.Access, "https://other.example/mcp") != 401 {
		t.Fatal("resource validation failed")
	}
	refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {reg.ClientID}, "refresh_token": {token.Refresh}, "resource": {issuer + "/mcp"}}
	w = call("/token", refresh)
	if w.Code != 200 {
		t.Fatalf("refresh: %d", w.Code)
	}
	var successor struct {
		Access string `json:"access_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &successor)
	if verify(successor.Access, issuer+"/mcp") != 200 {
		t.Fatal("successor invalid before reuse")
	}
	if call("/token", refresh).Code != 400 || verify(successor.Access, issuer+"/mcp") != 401 {
		t.Fatal("refresh replay did not revoke successor family")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM oauth_grants`).Scan(&count); err != nil || count != 0 {
		t.Fatal("revoked family still has grants")
	}
}
