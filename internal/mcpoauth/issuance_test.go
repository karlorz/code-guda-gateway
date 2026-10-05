package mcpoauth_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/mcpoauth"
	"code-guda-gateway/internal/store"
)

func setupTestOAuthServer(t *testing.T) (*mcpoauth.Server, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "oauth_issuance.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	pwHash, err := mcpoauth.HashPassword("operator-secret-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	srv := mcpoauth.NewServer("https://gateway.example", pwHash, st.DB())
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
	return srv, st.DB()
}

func registerTestClient(t *testing.T, srv *mcpoauth.Server, clientName string, redirectURIs []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"client_name":   clientName,
		"redirect_uris": redirectURIs,
	})
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	return res.ClientID
}

func TestIssuanceIdentity_OperatorAlwaysMintsOperator(t *testing.T) {
	srv, db := setupTestOAuthServer(t)

	// Set public_issuance to open
	_, err := db.Exec(`UPDATE settings SET value = 'open' WHERE key = 'public_issuance'`)
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}

	clientID := registerTestClient(t, srv, "MyOperatorAgent", []string{"https://client.example/callback"})

	// Verifier & challenge
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// Operator provides correct password
	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.example/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"password":              {"operator-secret-123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("authorize status=%d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	u, _ := url.Parse(loc)
	code := u.Query().Get("code")

	// Check oauth_codes row
	codeHash := gatewaykeys.HashRaw(code)
	var issuedVia, agentLabel string
	err = db.QueryRow(`SELECT issued_via, agent_label FROM oauth_codes WHERE code_hash = ?`, codeHash).Scan(&issuedVia, &agentLabel)
	if err != nil {
		t.Fatalf("query oauth_codes: %v", err)
	}
	if issuedVia != "operator" {
		t.Errorf("oauth_codes issued_via = %q, want 'operator'", issuedVia)
	}
	if agentLabel != "MyOperatorAgent" {
		t.Errorf("oauth_codes agent_label = %q, want 'MyOperatorAgent'", agentLabel)
	}

	// Exchange token
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://client.example/callback"},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	tReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tRec := httptest.NewRecorder()
	srv.ServeHTTP(tRec, tReq)
	if tRec.Code != http.StatusOK {
		t.Fatalf("token status=%d body=%s", tRec.Code, tRec.Body.String())
	}

	// Check gateway_keys row
	var gwIssuedVia, gwAgentLabel string
	err = db.QueryRow(`SELECT issued_via, agent_label FROM gateway_keys WHERE name = ?`, "oauth:MyOperatorAgent").Scan(&gwIssuedVia, &gwAgentLabel)
	if err != nil {
		t.Fatalf("query gateway_keys: %v", err)
	}
	if gwIssuedVia != "operator" {
		t.Errorf("gateway_keys issued_via = %q, want 'operator'", gwIssuedVia)
	}
	if gwAgentLabel != "MyOperatorAgent" {
		t.Errorf("gateway_keys agent_label = %q, want 'MyOperatorAgent'", gwAgentLabel)
	}
}

func TestIssuanceIdentity_OpenMintsWithoutPassword(t *testing.T) {
	srv, db := setupTestOAuthServer(t)

	_, err := db.Exec(`UPDATE settings SET value = 'open' WHERE key = 'public_issuance'`)
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}

	clientID := registerTestClient(t, srv, "PublicAgent", []string{"https://client.example/callback"})

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// No password supplied
	form := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.example/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"password":              {""},
	}
	req := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("authorize status=%d body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	u, _ := url.Parse(loc)
	code := u.Query().Get("code")

	// Check oauth_codes row
	codeHash := gatewaykeys.HashRaw(code)
	var issuedVia string
	err = db.QueryRow(`SELECT issued_via FROM oauth_codes WHERE code_hash = ?`, codeHash).Scan(&issuedVia)
	if err != nil {
		t.Fatalf("query oauth_codes: %v", err)
	}
	if issuedVia != "open_register" {
		t.Errorf("oauth_codes issued_via = %q, want 'open_register'", issuedVia)
	}

	// Exchange token
	tokenForm := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"https://client.example/callback"},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	tReq := httptest.NewRequest(http.MethodPost, "/token", strings.NewReader(tokenForm.Encode()))
	tReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tRec := httptest.NewRecorder()
	srv.ServeHTTP(tRec, tReq)
	if tRec.Code != http.StatusOK {
		t.Fatalf("token status=%d body=%s", tRec.Code, tRec.Body.String())
	}

	var gwIssuedVia string
	err = db.QueryRow(`SELECT issued_via FROM gateway_keys WHERE name = ?`, "oauth:PublicAgent").Scan(&gwIssuedVia)
	if err != nil {
		t.Fatalf("query gateway_keys: %v", err)
	}
	if gwIssuedVia != "open_register" {
		t.Errorf("gateway_keys issued_via = %q, want 'open_register'", gwIssuedVia)
	}
}

func TestIssuanceIdentity_RefCodeCases(t *testing.T) {
	srv, db := setupTestOAuthServer(t)

	_, err := db.Exec(`UPDATE settings SET value = 'ref_code' WHERE key = 'public_issuance'`)
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}

	inviteSvc := gatewaykeys.NewInviteService(db)
	exp := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)

	// Invite 1: bound to "BoundAgent", max 1
	inv1, err := inviteSvc.Create("BoundAgent", 1, exp)
	if err != nil {
		t.Fatalf("create invite 1: %v", err)
	}

	// Invite 2: expired
	pastExp := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	invExpired, err := inviteSvc.Create("", 1, pastExp)
	if err != nil {
		t.Fatalf("create expired invite: %v", err)
	}

	// Invite 3: revoked
	invRevoked, err := inviteSvc.Create("", 1, exp)
	if err != nil {
		t.Fatalf("create revokable invite: %v", err)
	}
	_ = inviteSvc.Revoke(invRevoked.ID)

	clientID := registerTestClient(t, srv, "BoundAgent", []string{"https://client.example/callback"})
	otherClientID := registerTestClient(t, srv, "OtherAgent", []string{"https://client.example/callback"})

	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// Case 1: Missing invite code -> 401
	formMissing := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.example/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	req1 := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(formMissing.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized || !strings.Contains(rec1.Body.String(), "Invite code required") {
		t.Fatalf("missing invite status=%d body=%s", rec1.Code, rec1.Body.String())
	}

	// Case 2: Expired invite -> 401
	formExpired := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.example/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"invite":                {invExpired.Code},
	}
	req2 := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(formExpired.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized || !strings.Contains(rec2.Body.String(), "expired") {
		t.Fatalf("expired invite status=%d body=%s", rec2.Code, rec2.Body.String())
	}

	// Case 3: Revoked invite -> 401
	formRevoked := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.example/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"invite":                {invRevoked.Code},
	}
	req3 := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(formRevoked.Encode()))
	req3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec3 := httptest.NewRecorder()
	srv.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusUnauthorized || !strings.Contains(rec3.Body.String(), "revoked") {
		t.Fatalf("revoked invite status=%d body=%s", rec3.Code, rec3.Body.String())
	}

	// Case 4: Wrong agent label bind -> 401
	formWrongBind := url.Values{
		"client_id":             {otherClientID},
		"redirect_uri":          {"https://client.example/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"invite":                {inv1.Code},
	}
	req4 := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(formWrongBind.Encode()))
	req4.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec4 := httptest.NewRecorder()
	srv.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusUnauthorized || !strings.Contains(rec4.Body.String(), "bound to agent label") {
		t.Fatalf("wrong bind status=%d body=%s", rec4.Code, rec4.Body.String())
	}

	// Case 5: Correct bind -> success, increments redemption count
	formOK := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {"https://client.example/callback"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"invite":                {inv1.Code},
	}
	req5 := httptest.NewRequest(http.MethodPost, "/authorize", strings.NewReader(formOK.Encode()))
	req5.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec5 := httptest.NewRecorder()
	srv.ServeHTTP(rec5, req5)
	if rec5.Code != http.StatusFound {
		t.Fatalf("correct bind status=%d body=%s", rec5.Code, rec5.Body.String())
	}

	// Verify redemption count incremented
	var count int
	_ = db.QueryRow(`SELECT redemption_count FROM invite_codes WHERE id = ?`, inv1.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("redemption_count = %d, want 1", count)
	}

	loc := rec5.Header().Get("Location")
	u, _ := url.Parse(loc)
	code := u.Query().Get("code")

	// Verify oauth_codes has ref_code_id
	codeHash := gatewaykeys.HashRaw(code)
	var issuedVia string
	var refCodeID int64
	err = db.QueryRow(`SELECT issued_via, ref_code_id FROM oauth_codes WHERE code_hash = ?`, codeHash).Scan(&issuedVia, &refCodeID)
	if err != nil {
		t.Fatalf("query oauth_codes: %v", err)
	}
	if issuedVia != "ref_code" || refCodeID != inv1.ID {
		t.Errorf("oauth_codes issued_via=%q refCodeID=%d, want 'ref_code' and %d", issuedVia, refCodeID, inv1.ID)
	}

	// Case 6: Exhausted invite -> subsequent redeem fails with 401
	rec6 := httptest.NewRecorder()
	srv.ServeHTTP(rec6, req5)
	if rec6.Code != http.StatusUnauthorized || !strings.Contains(rec6.Body.String(), "maximum redemptions") {
		t.Fatalf("exhausted invite status=%d body=%s", rec6.Code, rec6.Body.String())
	}
}
