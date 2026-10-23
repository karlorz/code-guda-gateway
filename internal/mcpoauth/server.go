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
	"sync"
	"time"

	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/providers"
)

type Server struct {
	issuer             string
	passwordHash       string
	db                 *sql.DB
	metadataClient     *http.Client
	registrationMu     sync.Mutex
	registrationWindow time.Time
	registrations      int
}

// NewServer returns an OAuth Server if both issuer and passwordHash are well-formed.
// Otherwise returns nil.
func NewServer(issuer, passwordHash string, db *sql.DB) *Server {
	cleanIssuer, ok := ValidateIssuer(issuer)
	if !ok {
		return nil
	}
	if db == nil {
		return nil
	}
	if err := ValidatePasswordHash(passwordHash); err != nil {
		return nil
	}
	// Bind only existing service-owned grants/codes during startup. Opaque
	// OAuth keys are already distinguished from provider/host credentials.
	if err := bindLegacyGrants(db, cleanIssuer+"/mcp"); err != nil {
		return nil
	}
	return &Server{
		metadataClient: newMetadataClient(),
		issuer:         cleanIssuer,
		passwordHash:   passwordHash,
		db:             db,
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
		"scopes_supported":         []string{"mcp", "offline_access", "config:write"},
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
		"client_id_metadata_document_supported": true,
		"code_challenge_methods_supported":      []string{"S256"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"response_types_supported":              []string{"code"},
		"scopes_supported":                      []string{"mcp", "offline_access", "config:write"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

const settingOAuthOperatorPasswordHash = providers.SettingOAuthOperatorPasswordHash
const settingPublicIssuance = "public_issuance"

func (s *Server) publicIssuanceMode() string {
	if s.db == nil {
		return "off"
	}
	var mode string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, settingPublicIssuance).Scan(&mode)
	if err != nil {
		return "off"
	}
	mode = strings.TrimSpace(mode)
	if mode == "open" || mode == "ref_code" {
		return mode
	}
	return "off"
}

func (s *Server) effectivePasswordHash() (string, error) {
	if s.db == nil {
		return s.passwordHash, nil
	}
	var stored string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, settingOAuthOperatorPasswordHash).Scan(&stored)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.passwordHash, nil
		}
		return "", err
	}
	if err := ValidatePasswordHash(stored); err != nil {
		return s.passwordHash, nil
	}
	return stored, nil
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
	if !s.allowRegistration() {
		w.Header().Set("Retry-After", "60")
		s.jsonOAuthError(w, http.StatusTooManyRequests, "temporarily_unavailable", "registration rate limit")
		return
	}
	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 16 || len(req.ClientName) > 256 {
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
	if strings.HasPrefix(clientID, "https://") {
		return s.fetchMetadataClient(clientID)
	}
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

func (s *Server) renderAuthorizeForm(w http.ResponseWriter, status int, client *oauthClient, redirectURI, state, codeChallenge, codeChallengeMethod, scope, resource, agentLabel, inviteCode, errMsg string) {
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
	escapedResource := html.EscapeString(resource)
	escapedAgentLabel := html.EscapeString(agentLabel)
	escapedInviteCode := html.EscapeString(inviteCode)

	errorSection := ""
	if errMsg != "" {
		errorSection = fmt.Sprintf(`<p style="color:#d93025;margin-bottom:1rem;font-weight:500;">%s</p>`, html.EscapeString(errMsg))
	}

	mode := s.publicIssuanceMode()

	var fieldsHTML string
	switch mode {
	case "open":
		// Mode is open: password input is hidden from primary flow, but operator can expand/fill or submit without password.
		// "Hide password input when mode is open (still allow POST password for operator)."
		fieldsHTML = fmt.Sprintf(`
    <p style="font-size:0.875rem;color:#38bdf8;margin-bottom:1rem;">Public access is open for this gateway.</p>
    <details style="margin-bottom:1.25rem;">
      <summary style="font-size:0.825rem;color:#94a3b8;cursor:pointer;">Operator sign-in (optional)</summary>
      <div style="margin-top:0.5rem;">
        <label for="password">Operator Password</label>
        <input type="password" id="password" name="password" autocomplete="current-password">
      </div>
    </details>`)
	case "ref_code":
		// When ref_code, show invite input. If invite= query prefilled, value is set. Operator password still shown.
		inviteReq := "required"
		fieldsHTML = fmt.Sprintf(`
    <label for="invite">Invite Code</label>
    <input type="text" id="invite" name="invite" value="%s" %s placeholder="inv_..." style="width:100%%;box-sizing:border-box;padding:0.625rem;border-radius:6px;border:1px solid #475569;background:#0f172a;color:#fff;font-size:1rem;margin-bottom:1rem;">
    <details style="margin-bottom:1.25rem;">
      <summary style="font-size:0.825rem;color:#94a3b8;cursor:pointer;">Operator override</summary>
      <div style="margin-top:0.5rem;">
        <label for="password">Operator Password</label>
        <input type="password" id="password" name="password" autocomplete="current-password">
      </div>
    </details>`, escapedInviteCode, inviteReq)
	default: // "off"
		fieldsHTML = `
    <label for="password">Operator Password</label>
    <input type="password" id="password" name="password" required autofocus autocomplete="current-password">`
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
input[type="password"], input[type="text"] { width: 100%%; box-sizing: border-box; padding: 0.625rem; border-radius: 6px; border: 1px solid #475569; background: #0f172a; color: #fff; font-size: 1rem; margin-bottom: 1.25rem; }
button { width: 100%%; padding: 0.625rem; border-radius: 6px; border: none; background: #3b82f6; color: #fff; font-size: 1rem; font-weight: 500; cursor: pointer; }
button:hover { background: #2563eb; }
</style>
</head>
<body>
<div class="card">
  <h1>Authorize %s</h1>
  <p>Authorize <strong>%s</strong> to access Model Context Protocol tools via this gateway.</p>
  <p>Client identity: <strong>%s</strong></p>
  <p>Requested permissions: <strong>%s</strong></p>
  <p>This operator password is set at /admin after admin sign-in and is not the admin token.</p>
  %s
  <form method="POST" action="/authorize">
    <input type="hidden" name="client_id" value="%s">
    <input type="hidden" name="redirect_uri" value="%s">
    <input type="hidden" name="state" value="%s">
    <input type="hidden" name="code_challenge" value="%s">
    <input type="hidden" name="code_challenge_method" value="%s">
    <input type="hidden" name="scope" value="%s">
    <input type="hidden" name="resource" value="%s">
    <input type="hidden" name="agent_label" value="%s">
    %s
    <button type="submit">Approve Access</button>
  </form>
</div>
</body>
</html>`, escapedDisplayName, escapedDisplayName, escapedClientID, escapedScope, errorSection, escapedClientID, escapedRedirectURI, escapedState, escapedCodeChallenge, escapedCodeChallengeMethod, escapedScope, escapedResource, escapedAgentLabel, fieldsHTML)

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
	scope, ok := normalizeScope(q.Get("scope"))
	resource := s.issuer + "/mcp"
	if !ok {
		s.jsonOAuthError(w, 400, "invalid_scope", "unsupported scope")
		return
	}
	if !s.validResource(q["resource"]) {
		s.jsonOAuthError(w, 400, "invalid_target", "invalid resource")
		return
	}
	inviteCode := q.Get("invite")
	agentLabel := q.Get("agent_label")

	client, err := s.lookupClient(clientID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if client == nil {
		http.Error(w, "invalid client_id", http.StatusBadRequest)
		return
	}

	if agentLabel == "" && client.ClientName != "" {
		agentLabel = strings.TrimSpace(client.ClientName)
	}

	validRedirect := false
	for _, u := range client.RedirectURIs {
		if redirectMatches(u, redirectURI) {
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

	s.renderAuthorizeForm(w, http.StatusOK, client, redirectURI, state, codeChallenge, codeChallengeMethod, scope, resource, agentLabel, inviteCode, "")
}

// POST /authorize
func (s *Server) handleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form data", http.StatusBadRequest)
		return
	}
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	state := r.FormValue("state")
	codeChallenge := r.FormValue("code_challenge")
	codeChallengeMethod := r.FormValue("code_challenge_method")
	scope, ok := normalizeScope(r.FormValue("scope"))
	resource := s.issuer + "/mcp"
	if !ok {
		s.jsonOAuthError(w, 400, "invalid_scope", "unsupported scope")
		return
	}
	if !s.validResource(r.Form["resource"]) {
		s.jsonOAuthError(w, 400, "invalid_target", "invalid resource")
		return
	}
	password := r.FormValue("password")
	agentLabel := strings.TrimSpace(r.FormValue("agent_label"))
	inviteCode := strings.TrimSpace(r.FormValue("invite"))

	client, err := s.lookupClient(clientID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if client == nil {
		http.Error(w, "invalid client_id", http.StatusBadRequest)
		return
	}

	if agentLabel == "" && client.ClientName != "" {
		agentLabel = strings.TrimSpace(client.ClientName)
	}

	validRedirect := false
	for _, u := range client.RedirectURIs {
		if redirectMatches(u, redirectURI) {
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

	expectedHash, err := s.effectivePasswordHash()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	operatorApproved := password != "" && VerifyPassword(password, expectedHash)
	if hasScope(scope, "config:write") && !operatorApproved {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_scope", "configuration permission requires operator consent")
		return
	}
	mode := s.publicIssuanceMode()

	var issuedVia string
	var refCodeID *int64

	// Mint decision logic:
	// 1. If operator password verifies -> issued_via=operator (even when public is open/ref_code).
	// 2. Else if public_issuance=open -> issued_via=open_register (password may be empty).
	// 3. Else if public_issuance=ref_code -> require invite form/query; atomic redeem.
	//    If bind nonempty, redeeming agent_label must match bind. issued_via=ref_code, store ref_code_id on oauth_codes.
	// 4. Else invalid password as today.

	if operatorApproved {
		issuedVia = "operator"
	} else if mode == "open" {
		issuedVia = "open_register"
	} else if mode == "ref_code" {
		if inviteCode == "" {
			s.renderAuthorizeForm(w, http.StatusUnauthorized, client, redirectURI, state, codeChallenge, codeChallengeMethod, scope, resource, agentLabel, inviteCode, "Invite code required.")
			return
		}
		redeemed, err := gatewaykeys.RedeemTx(s.db, inviteCode, agentLabel)
		if err != nil {
			var msg string
			switch {
			case errors.Is(err, gatewaykeys.ErrInviteNotFound):
				msg = "Invalid invite code."
			case errors.Is(err, gatewaykeys.ErrInviteRevoked):
				msg = "This invite code has been revoked."
			case errors.Is(err, gatewaykeys.ErrInviteExpired):
				msg = "This invite code has expired."
			case errors.Is(err, gatewaykeys.ErrInviteExhausted):
				msg = "This invite code has reached its maximum redemptions."
			case errors.Is(err, gatewaykeys.ErrInviteLabelBound):
				msg = fmt.Sprintf("Invite code is bound to agent label %q.", redeemedBindHint(s.db, inviteCode))
			default:
				msg = "Failed to redeem invite code."
			}
			s.renderAuthorizeForm(w, http.StatusUnauthorized, client, redirectURI, state, codeChallenge, codeChallengeMethod, scope, resource, agentLabel, inviteCode, msg)
			return
		}
		issuedVia = "ref_code"
		refCodeID = &redeemed.ID
	} else {
		// Public issuance is off or operator password incorrect
		s.renderAuthorizeForm(w, http.StatusUnauthorized, client, redirectURI, state, codeChallenge, codeChallengeMethod, scope, resource, agentLabel, inviteCode, "Invalid operator password. Please try again.")
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
		INSERT INTO oauth_codes (code_hash, client_id, redirect_uri, code_challenge, code_challenge_method, expires_at, created_at, issued_via, agent_label, ref_code_id, resource, scope)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		codeHash, clientID, redirectURI, codeChallenge, codeChallengeMethod, expiresAt, createdAt, issuedVia, agentLabel, refCodeID, resource, scope,
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

func redeemedBindHint(db *sql.DB, code string) string {
	var bind string
	_ = db.QueryRow(`SELECT agent_label_bind FROM invite_codes WHERE code = ?`, code).Scan(&bind)
	return bind
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if err := r.ParseForm(); err != nil {
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form data")
		return
	}
	if !s.validResource(r.Form["resource"]) {
		s.jsonOAuthError(w, 400, "invalid_target", "resource must identify this MCP server")
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
	var rowIssuedVia, rowAgentLabel, resource, scope string
	var rowRefCodeID sql.NullInt64
	err = txCode.QueryRow(`
		SELECT client_id, redirect_uri, code_challenge, code_challenge_method, expires_at, issued_via, agent_label, ref_code_id, resource, scope
		FROM oauth_codes WHERE code_hash = ?`, codeHash,
	).Scan(&rowClientID, &rowRedirectURI, &rowCodeChallenge, &rowCodeChallengeMethod, &rowExpiresAt, &rowIssuedVia, &rowAgentLabel, &rowRefCodeID, &resource, &scope)
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

	if resource != s.issuer+"/mcp" || rowClientID != clientID || rowRedirectURI != redirectURI {
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
	keyName := "oauth:" + rowAgentLabel
	if clientName.Valid && strings.TrimSpace(clientName.String) != "" {
		keyName = "oauth:" + strings.TrimSpace(clientName.String)
	}

	if rowAgentLabel == "" {
		if clientName.Valid && strings.TrimSpace(clientName.String) != "" {
			rowAgentLabel = strings.TrimSpace(clientName.String)
		} else {
			rowAgentLabel = clientID
		}
	}
	if rowIssuedVia == "" {
		rowIssuedVia = "operator"
	}
	var refCodeIDPtr *int64
	if rowRefCodeID.Valid {
		refCodeIDPtr = &rowRefCodeID.Int64
	}

	// Mint gateway key inside tx
	rawKey, displayKey, err := gatewaykeys.CreateOAuthTxWithIssuance(tx, keyName, rowIssuedVia, rowAgentLabel, refCodeIDPtr)
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
	familyID := refreshHash

	_, err = tx.Exec(`
		INSERT INTO oauth_grants (refresh_hash, gateway_key_id, client_id, access_expires_at, refresh_expires_at, created_at, resource, scope, family_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		refreshHash, displayKey.ID, clientID, accessExpiresAt, refreshExpiresAt, createdAt, resource, scope, familyID,
	)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if _, err := tx.Exec(`INSERT INTO oauth_refresh_history VALUES (?, ?, ?, ?)`, refreshHash, familyID, displayKey.ID, refreshExpiresAt); err != nil {
		http.Error(w, "internal error", 500)
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
		"scope":         scope,
	}
	if !hasScope(scope, "offline_access") {
		delete(resp, "refresh_token")
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
	var grantClientID, refreshExpiresAtStr, resource, scope, familyID string
	err = tx.QueryRow(`
		SELECT gateway_key_id, client_id, refresh_expires_at, resource, scope, family_id
		FROM oauth_grants WHERE refresh_hash = ?`, refreshHash,
	).Scan(&oldKeyID, &grantClientID, &refreshExpiresAtStr, &resource, &scope, &familyID)
	if errors.Is(err, sql.ErrNoRows) {
		// Retained hashes make a replay revoke every successor, not just fail
		// lookup. Never retain or log raw refresh credentials.
		var reusedFamily, expires string
		if tx.QueryRow(`SELECT family_id, expires_at FROM oauth_refresh_history WHERE refresh_hash = ?`, refreshHash).Scan(&reusedFamily, &expires) == nil {
			if exp, e := time.Parse(time.RFC3339Nano, expires); e == nil && time.Now().UTC().Before(exp) {
				if _, e = tx.Exec(`UPDATE gateway_keys SET enabled = 0 WHERE id IN (SELECT gateway_key_id FROM oauth_refresh_history WHERE family_id = ?)`, reusedFamily); e != nil {
					http.Error(w, "internal error", 500)
					return
				}
				if _, e = tx.Exec(`DELETE FROM oauth_grants WHERE family_id = ?`, reusedFamily); e != nil {
					http.Error(w, "internal error", 500)
					return
				}
				if e = tx.Commit(); e != nil {
					http.Error(w, "internal error", 500)
					return
				}
			}
		}
		s.jsonOAuthError(w, http.StatusBadRequest, "invalid_grant", "invalid refresh token")
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if resource != s.issuer+"/mcp" || !hasScope(scope, "offline_access") || (clientID != "" && grantClientID != clientID) {
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
	var oldIssuedVia, oldAgentLabel sql.NullString
	var oldRefCodeID sql.NullInt64
	err = tx.QueryRow(`
		SELECT name, enabled, revoked_at, issued_via, agent_label, ref_code_id
		FROM gateway_keys WHERE id = ?`, oldKeyID,
	).Scan(&keyName, &enabled, &revokedAt, &oldIssuedVia, &oldAgentLabel, &oldRefCodeID)
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

	succIssuedVia := "operator"
	if oldIssuedVia.Valid && oldIssuedVia.String != "" {
		succIssuedVia = oldIssuedVia.String
	}
	succAgentLabel := keyName
	if oldAgentLabel.Valid && oldAgentLabel.String != "" {
		succAgentLabel = oldAgentLabel.String
	}
	var succRefCodeID *int64
	if oldRefCodeID.Valid {
		succRefCodeID = &oldRefCodeID.Int64
	}

	// Mint successor key preserving issuance metadata
	rawSuccessorKey, displaySuccessorKey, err := gatewaykeys.CreateOAuthTxWithIssuance(tx, keyName, succIssuedVia, succAgentLabel, succRefCodeID)
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
	newRefreshExpiresAt := refreshExpiresAtStr // absolute family lifetime, not a sliding expiry
	createdAt := now.Format(time.RFC3339Nano)

	_, err = tx.Exec(`
		INSERT INTO oauth_grants (refresh_hash, gateway_key_id, client_id, access_expires_at, refresh_expires_at, created_at, resource, scope, family_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newRefreshHash, displaySuccessorKey.ID, grantClientID, accessExpiresAt, newRefreshExpiresAt, createdAt, resource, scope, familyID,
	)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if _, err := tx.Exec(`INSERT INTO oauth_refresh_history VALUES (?, ?, ?, ?)`, newRefreshHash, familyID, displaySuccessorKey.ID, newRefreshExpiresAt); err != nil {
		http.Error(w, "internal error", 500)
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
		"scope":         scope,
	}
	if !hasScope(scope, "offline_access") {
		delete(resp, "refresh_token")
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

// Missing resource remains compatible for older single-resource clients. An
// explicit target must match exactly; every persisted grant has a target.
func (s *Server) validResource(values []string) bool {
	return len(values) == 0 || (len(values) == 1 && values[0] == s.issuer+"/mcp")
}

func hasScope(scope, want string) bool {
	for _, v := range strings.Fields(scope) {
		if v == want {
			return true
		}
	}
	return false
}

func normalizeScope(raw string) (string, bool) {
	if raw == "" {
		return "mcp offline_access", true
	} // existing client default
	seen := map[string]bool{}
	for _, v := range strings.Fields(raw) {
		if v != "mcp" && v != "offline_access" && v != "config:write" {
			return "", false
		}
		seen[v] = true
	}
	if !seen["mcp"] {
		return "", false
	}
	out := []string{"mcp"}
	for _, v := range []string{"offline_access", "config:write"} {
		if seen[v] {
			out = append(out, v)
		}
	}
	return strings.Join(out, " "), true
}

func (s *Server) ResourceURL() string { return s.issuer + "/mcp" }

// A global bound is intentional: Caddy is the only peer on the production
// listener, and untrusted forwarding headers must not bypass registration limits.
func (s *Server) allowRegistration() bool {
	s.registrationMu.Lock()
	defer s.registrationMu.Unlock()
	if time.Since(s.registrationWindow) >= time.Minute {
		s.registrationWindow = time.Now()
		s.registrations = 0
	}
	if s.registrations >= 60 {
		return false
	}
	s.registrations++
	return true
}
