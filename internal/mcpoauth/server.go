package mcpoauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"code-guda-gateway/internal/gatewaykeys"
)

type Server struct {
	issuer       string
	passwordHash string
	db           *sql.DB
}

// NewServer returns an OAuth Server if both issuer and passwordHash are well-formed.
// Otherwise returns nil.
func NewServer(issuer, passwordHash string, db *sql.DB) *Server {
	cleanIssuer, ok := ValidateIssuer(issuer)
	if !ok {
		return nil
	}
	if err := ValidatePasswordHash(passwordHash); err != nil {
		return nil
	}
	return &Server{
		issuer:       cleanIssuer,
		passwordHash: passwordHash,
		db:           db,
	}
}

// IsOAuthRoute reports whether the incoming request matches an OAuth endpoint.
func (s *Server) IsOAuthRoute(r *http.Request) bool {
	path := r.URL.Path
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		(path == "/.well-known/oauth-protected-resource" || path == "/.well-known/oauth-protected-resource/mcp") {
		return true
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		path == "/.well-known/oauth-authorization-server" {
		return true
	}
	if r.Method == http.MethodPost && path == "/register" {
		return true
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodPost) && path == "/authorize" {
		return true
	}
	if r.Method == http.MethodPost && path == "/token" {
		return true
	}
	return false
}

// ServeHTTP dispatches requests to OAuth endpoints.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		(path == "/.well-known/oauth-protected-resource" || path == "/.well-known/oauth-protected-resource/mcp"):
		s.handleProtectedResource(w, r)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		path == "/.well-known/oauth-authorization-server":
		s.handleAuthServerMeta(w, r)
	case r.Method == http.MethodPost && path == "/register":
		s.handleRegister(w, r)
	case r.Method == http.MethodGet && path == "/authorize":
		s.handleAuthorizeGet(w, r)
	case r.Method == http.MethodPost && path == "/authorize":
		s.handleAuthorizePost(w, r)
	case r.Method == http.MethodPost && path == "/token":
		s.handleToken(w, r)
	default:
		http.NotFound(w, r)
	}
}

// GET /.well-known/oauth-protected-resource and GET /.well-known/oauth-protected-resource/mcp
func (s *Server) handleProtectedResource(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"resource":                 s.issuer + "/mcp",
		"authorization_servers":    []string{s.issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{"mcp", "offline_access"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// GET /.well-known/oauth-authorization-server
func (s *Server) handleAuthServerMeta(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"issuer":                                s.issuer,
		"authorization_endpoint":                s.issuer + "/authorize",
		"token_endpoint":                        s.issuer + "/token",
		"registration_endpoint":                 s.issuer + "/register",
		"code_challenge_methods_supported":      []string{"S256"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"response_types_supported":              []string{"code"},
		"scopes_supported":                      []string{"mcp", "offline_access"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type registerRequest struct {
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
}

type registerResponse struct {
	ClientID     string   `json:"client_id"`
	ClientName   string   `json:"client_name,omitempty"`
	RedirectURIs []string `json:"redirect_uris"`
}

// POST /register
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	if len(req.RedirectURIs) == 0 {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !ValidateRedirectURI(u) {
			s.jsonOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect uri must be https or loopback http")
			return
		}
	}

	clientIDBytes := make([]byte, 16)
	if _, err := rand.Read(clientIDBytes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	clientID := hex.EncodeToString(clientIDBytes)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	urisJSON, err := json.Marshal(req.RedirectURIs)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_, err = s.db.Exec(
		`INSERT INTO oauth_clients (client_id, client_name, redirect_uris, created_at) VALUES (?, ?, ?, ?)`,
		clientID, req.ClientName, string(urisJSON), now,
	)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := registerResponse{
		ClientID:     clientID,
		ClientName:   req.ClientName,
		RedirectURIs: req.RedirectURIs,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

type oauthClient struct {
	ClientID     string
	ClientName   string
	RedirectURIs []string
}

func (s *Server) lookupClient(clientID string) (*oauthClient, error) {
	var c oauthClient
	var urisJSON string
	var name sql.NullString
	err := s.db.QueryRow(`SELECT client_id, client_name, redirect_uris FROM oauth_clients WHERE client_id = ?`, clientID).Scan(
		&c.ClientID, &name, &urisJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if name.Valid {
		c.ClientName = name.String
	}
	if err := json.Unmarshal([]byte(urisJSON), &c.RedirectURIs); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Server) renderAuthorizeForm(w http.ResponseWriter, status int, client *oauthClient, redirectURI, state, codeChallenge, codeChallengeMethod, scope, errMsg string) {
	displayName := "this client"
	if client != nil && strings.TrimSpace(client.ClientName) != "" {
		displayName = strings.TrimSpace(client.ClientName)
	}
	escapedDisplayName := html.EscapeString(displayName)
	escapedRedirectURI := html.EscapeString(redirectURI)
	escapedState := html.EscapeString(state)
	escapedCodeChallenge := html.EscapeString(codeChallenge)
	escapedCodeChallengeMethod := html.EscapeString(codeChallengeMethod)
	escapedClientID := ""
	if client != nil {
		escapedClientID = html.EscapeString(client.ClientID)
	}
	escapedScope := html.EscapeString(scope)

	errorSection := ""
	if errMsg != "" {
		errorSection = fmt.Sprintf(`<p style="color:#d93025;margin-bottom:1rem;font-weight:500;">%s</p>`, html.EscapeString(errMsg))
	}

	doc := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Authorize Access</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #0f172a; color: #f8fafc; display: flex; align-items: center; justify-content: center; min-height: 100vh; margin: 0; padding: 1rem; }
.card { background: #1e293b; border: 1px solid #334155; border-radius: 8px; max-width: 440px; width: 100%%; padding: 2rem; box-shadow: 0 4px 6px -1px rgba(0,0,0,0.2); }
h1 { font-size: 1.25rem; margin-top: 0; margin-bottom: 1rem; color: #f1f5f9; }
p { font-size: 0.925rem; color: #94a3b8; line-height: 1.5; margin-top: 0; }
label { display: block; font-size: 0.875rem; margin-bottom: 0.5rem; color: #cbd5e1; font-weight: 500; }
input[type="password"] { width: 100%%; box-sizing: border-box; padding: 0.625rem; border-radius: 6px; border: 1px solid #475569; background: #0f172a; color: #fff; font-size: 1rem; margin-bottom: 1.25rem; }
button { width: 100%%; padding: 0.625rem; border-radius: 6px; border: none; background: #3b82f6; color: #fff; font-size: 1rem; font-weight: 500; cursor: pointer; }
button:hover { background: #2563eb; }
</style>
</head>
<body>
<div class="card">
  <h1>Authorize %s</h1>
  <p>Authorize <strong>%s</strong> to access Model Context Protocol tools via this gateway.</p>
  %s
  <form method="POST" action="/authorize">
    <input type="hidden" name="client_id" value="%s">
    <input type="hidden" name="redirect_uri" value="%s">
    <input type="hidden" name="state" value="%s">
    <input type="hidden" name="code_challenge" value="%s">
    <input type="hidden" name="code_challenge_method" value="%s">
    <input type="hidden" name="scope" value="%s">
    <label for="password">Operator Password</label>
    <input type="password" id="password" name="password" required autofocus autocomplete="current-password">
    <button type="submit">Approve Access</button>
  </form>
</div>
</body>
</html>`, escapedDisplayName, escapedDisplayName, errorSection, escapedClientID, escapedRedirectURI, escapedState, escapedCodeChallenge, escapedCodeChallengeMethod, escapedScope)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(doc))
}

// GET /authorize
func (s *Server) handleAuthorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	responseType := q.Get("response_type")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")
	state := q.Get("state")
	scope := q.Get("scope")

	client, err := s.lookupClient(clientID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if client == nil {
		http.Error(w, "invalid client_id", http.StatusBadRequest)
		return
	}

	validRedirect := false
	for _, u := range client.RedirectURIs {
		if u == redirectURI {
			validRedirect = true
			break
		}
	}
	if !validRedirect {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}

	if responseType != "code" {
		http.Error(w, "unsupported response_type: must be code", http.StatusBadRequest)
		return
	}
	if codeChallengeMethod != "S256" || codeChallenge == "" {
		http.Error(w, "unsupported code_challenge_method: must be S256", http.StatusBadRequest)
		return
	}

	s.renderAuthorizeForm(w, http.StatusOK, client, redirectURI, state, codeChallenge, codeChallengeMethod, scope, "")
}

// POST /authorize
func (s *Server) handleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	state := r.FormValue("state")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")
	scope := r.FormValue("scope")
	password := r.FormValue("password")

	client, err := s.lookupClient(clientID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if client == nil {
		http.Error(w, "invalid client_id", http.StatusBadRequest)
		return
	}

	validRedirect := false
	for _, u := range client.RedirectURIs {
		if u == redirectURI {
			validRedirect = true
			break
		}
	}
	if !validRedirect {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}

	if codeChallengeMethod != "S256" || codeChallenge == "" {
		http.Error(w, "unsupported code_challenge_method", http.StatusBadRequest)
		return
	}

	if !VerifyPassword(password, s.passwordHash) {
		s.renderAuthorizeForm(w, http.StatusUnauthorized, client, redirectURI, state, codeChallenge, codeChallengeMethod, scope, "Invalid operator password. Please try again.")
		return
	}

	// Generate authorization code
	codeBytes := make([]byte, 32)
	if _, err := rand.Read(codeBytes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rawCode := base64.RawURLEncoding.EncodeToString(codeBytes)
	codeHash := gatewaykeys.HashRaw(rawCode)

	now := time.Now().UTC()
	createdAt := now.Format(time.RFC3339Nano)
	expiresAt := now.Add(5 * time.Minute).Format(time.RFC3339Nano)

	_, err = s.db.Exec(`
		INSERT INTO oauth_codes (code_hash, client_id, redirect_uri, code_challenge, code_challenge_method, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		codeHash, clientID, redirectURI, codeChallenge, codeChallengeMethod, expiresAt, createdAt,
	)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	vals := u.Query()
	vals.Set("code", rawCode)
	if state != "" {
		vals.Set("state", state)
	}
	u.RawQuery = vals.Encode()

	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form data")
		return
	}
	grantType := r.FormValue("grant_type")
	switch grantType {
	case "authorization_code":
		s.handleTokenAuthCode(w, r)
	case "refresh_token":
		s.handleTokenRefreshToken(w, r)
	default:
		s.jsonOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "unsupported grant_type")
	}
}

// POST /token authorization_code
func (s *Server) handleTokenAuthCode(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")
	clientID := r.FormValue("client_id")
	codeVerifier := r.FormValue("code_verifier")

	if code == "" || redirectURI == "" || clientID == "" || codeVerifier == "" {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_request", "missing required fields")
		return
	}

	codeHash := gatewaykeys.HashRaw(code)

	// Step 1: In an atomic transaction, read AND delete the code row.
	// This ensures the code cannot be reused even if verification or downstream minting fails.
	txCode, err := s.db.Begin()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var rowClientID, rowRedirectURI, rowCodeChallenge, rowCodeChallengeMethod, rowExpiresAt string
	err = txCode.QueryRow(`
		SELECT client_id, redirect_uri, code_challenge, code_challenge_method, expires_at
		FROM oauth_codes WHERE code_hash = ?`, codeHash,
	).Scan(&rowClientID, &rowRedirectURI, &rowCodeChallenge, &rowCodeChallengeMethod, &rowExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		_ = txCode.Rollback()
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid or expired code")
		return
	}
	if err != nil {
		_ = txCode.Rollback()
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, err := txCode.Exec(`DELETE FROM oauth_codes WHERE code_hash = ?`, codeHash); err != nil {
		_ = txCode.Rollback()
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := txCode.Commit(); err != nil {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "concurrent code consumption")
		return
	}

	exp, err := time.Parse(time.RFC3339Nano, rowExpiresAt)
	if err != nil || time.Now().UTC().After(exp) {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "code expired")
		return
	}

	if rowClientID != clientID || rowRedirectURI != redirectURI {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id or redirect_uri mismatch")
		return
	}

	// Verify PKCE S256
	if rowCodeChallengeMethod != "S256" {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "unsupported code challenge method")
		return
	}
	verifierHash := sha256.Sum256([]byte(codeVerifier))
	computedChallenge := base64.RawURLEncoding.EncodeToString(verifierHash[:])
	if subtle.ConstantTimeCompare([]byte(computedChallenge), []byte(rowCodeChallenge)) != 1 {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "code verifier does not match code challenge")
		return
	}

	// Step 2: Mint key and grant in a transaction
	tx, err := s.db.Begin()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// Get client details for key name
	var clientName sql.NullString
	_ = tx.QueryRow(`SELECT client_name FROM oauth_clients WHERE client_id = ?`, clientID).Scan(&clientName)
	keyName := "oauth:" + clientID
	if clientName.Valid && strings.TrimSpace(clientName.String) != "" {
		keyName = "oauth:" + strings.TrimSpace(clientName.String)
	}

	// Mint gateway key inside tx
	rawKey, displayKey, err := gatewaykeys.CreateOAuthTx(tx, keyName)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Mint refresh token
	refreshBytes := make([]byte, 32)
	if _, err := rand.Read(refreshBytes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rawRefreshToken := base64.RawURLEncoding.EncodeToString(refreshBytes)
	refreshHash := gatewaykeys.HashRaw(rawRefreshToken)

	now := time.Now().UTC()
	accessExpiresAt := now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	refreshExpiresAt := now.Add(14 * 24 * time.Hour).Format(time.RFC3339Nano)
	createdAt := now.Format(time.RFC3339Nano)

	_, err = tx.Exec(`
		INSERT INTO oauth_grants (refresh_hash, gateway_key_id, client_id, access_expires_at, refresh_expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		refreshHash, displayKey.ID, clientID, accessExpiresAt, refreshExpiresAt, createdAt,
	)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"access_token":  rawKey,
		"token_type":    "Bearer",
		"expires_in":    7200,
		"refresh_token": rawRefreshToken,
		"scope":         "mcp offline_access",
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// POST /token refresh_token
func (s *Server) handleTokenRefreshToken(w http.ResponseWriter, r *http.Request) {
	refreshToken := r.FormValue("refresh_token")
	clientID := r.FormValue("client_id")

	if refreshToken == "" {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token required")
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	refreshHash := gatewaykeys.HashRaw(refreshToken)

	// Single atomic check and delete of old refresh grant
	var oldKeyID int64
	var grantClientID, refreshExpiresAtStr string
	err = tx.QueryRow(`
		SELECT gateway_key_id, client_id, refresh_expires_at
		FROM oauth_grants WHERE refresh_hash = ?`, refreshHash,
	).Scan(&oldKeyID, &grantClientID, &refreshExpiresAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid refresh token")
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if clientID != "" && grantClientID != clientID {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "client_id mismatch")
		return
	}

	// Delete old refresh hash in this transaction (concurrent refresh will get sql.ErrNoRows or zero rows affected)
	res, err := tx.Exec(`DELETE FROM oauth_grants WHERE refresh_hash = ?`, refreshHash)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	affected, err := res.RowsAffected()
	if err != nil || affected != 1 {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token already consumed")
		return
	}

	// Check refresh token expiration
	refreshExp, err := time.Parse(time.RFC3339Nano, refreshExpiresAtStr)
	if err != nil || time.Now().UTC().After(refreshExp) {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token expired")
		return
	}

	// Check old gateway key status: must be enabled and not revoked
	var keyName string
	var enabled int
	var revokedAt sql.NullString
	err = tx.QueryRow(`
		SELECT name, enabled, revoked_at
		FROM gateway_keys WHERE id = ?`, oldKeyID,
	).Scan(&keyName, &enabled, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) || enabled == 0 || revokedAt.Valid {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "gateway key revoked or disabled")
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Disable old gateway key
	if err := gatewaykeys.DisableTx(tx, oldKeyID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Mint successor key
	rawSuccessorKey, displaySuccessorKey, err := gatewaykeys.CreateOAuthTx(tx, keyName)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Mint successor refresh token
	newRefreshBytes := make([]byte, 32)
	if _, err := rand.Read(newRefreshBytes); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	newRawRefreshToken := base64.RawURLEncoding.EncodeToString(newRefreshBytes)
	newRefreshHash := gatewaykeys.HashRaw(newRawRefreshToken)

	now := time.Now().UTC()
	accessExpiresAt := now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	newRefreshExpiresAt := now.Add(14 * 24 * time.Hour).Format(time.RFC3339Nano)
	createdAt := now.Format(time.RFC3339Nano)

	_, err = tx.Exec(`
		INSERT INTO oauth_grants (refresh_hash, gateway_key_id, client_id, access_expires_at, refresh_expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		newRefreshHash, displaySuccessorKey.ID, grantClientID, accessExpiresAt, newRefreshExpiresAt, createdAt,
	)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "concurrent update conflict")
		return
	}

	resp := map[string]any{
		"access_token":  rawSuccessorKey,
		"token_type":    "Bearer",
		"expires_in":    7200,
		"refresh_token": newRawRefreshToken,
		"scope":         "mcp offline_access",
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) jsonOAuthError(w http.ResponseWriter, status int, errCode, errDesc string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             errCode,
		"error_description": errDesc,
	})
}
