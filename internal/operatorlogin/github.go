// Package operatorlogin authenticates explicitly allowlisted GitHub operators.
// Its proofs authorize only a single, bound MCP consent submission, never admin access.
package operatorlogin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"code-guda-gateway/internal/secrets"
)

const settingKey = "oauth_github_operator"
const CookieName = "guda_operator_browser"
const CallbackPath = "/authorize/github/callback"
const ttl = 10 * time.Minute

var ErrDenied = errors.New("GitHub operator sign-in failed; retry sign-in or use the operator password")
var ErrConfig = errors.New("configure a GitHub client ID, client secret and at least one allowed numeric user ID before enabling")

type configuration struct {
	Enabled    bool     `json:"enabled"`
	ClientID   string   `json:"client_id"`
	Secret     string   `json:"secret_ciphertext,omitempty"`
	AllowedIDs []string `json:"allowed_user_ids"`
	Revision   string   `json:"revision"`
}

type Status struct {
	Enabled          bool     `json:"enabled"`
	ClientID         string   `json:"client_id"`
	SecretConfigured bool     `json:"secret_configured"`
	AllowedIDs       []string `json:"allowed_user_ids"`
	CallbackURL      string   `json:"callback_url"`
	Available        bool     `json:"available"`
}

type Update struct {
	Enabled      bool     `json:"enabled"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	AllowedIDs   []string `json:"allowed_user_ids"`
}

type Service struct {
	db     *sql.DB
	key    []byte
	issuer string
	client *http.Client
}

func New(db *sql.DB, key []byte, issuer string) *Service {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Service{db: db, key: bytes.Clone(key), issuer: issuer, client: &http.Client{
		Timeout: 10 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (s *Service) load() (configuration, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, settingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return configuration{AllowedIDs: []string{}}, nil
	}
	if err != nil {
		return configuration{}, err
	}
	var c configuration
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return configuration{}, err
	}
	return c, nil
}

func (s *Service) Status() (Status, error) {
	c, err := s.load()
	if err != nil {
		return Status{}, err
	}
	ids := c.AllowedIDs
	if ids == nil {
		ids = []string{}
	}
	available := s.issuer != "" && len(s.key) == 32
	callback := ""
	if available {
		callback = s.issuer + CallbackPath
	}
	return Status{Enabled: c.Enabled, ClientID: c.ClientID, SecretConfigured: c.Secret != "", AllowedIDs: ids, CallbackURL: callback, Available: available}, nil
}

func (s *Service) Save(u Update) error {
	c, err := s.load()
	if err != nil {
		return err
	}
	u.ClientID = strings.TrimSpace(u.ClientID)
	if len(u.ClientID) > 256 || strings.ContainsAny(u.ClientID, "\r\n\t ") {
		return ErrConfig
	}
	if len(u.ClientSecret) > 4096 || len(u.AllowedIDs) > 100 {
		return ErrConfig
	}
	ids := make([]string, 0, len(u.AllowedIDs))
	seen := map[string]bool{}
	for _, v := range u.AllowedIDs {
		v = strings.TrimSpace(v)
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != v {
			return ErrConfig
		}
		if !seen[v] {
			ids = append(ids, v)
			seen[v] = true
		}
	}
	sort.Strings(ids)
	// A secret cannot be silently retained for a different OAuth app.
	if u.ClientID != c.ClientID && u.ClientSecret == "" {
		c.Secret = ""
	}
	if u.ClientSecret != "" {
		if strings.TrimSpace(u.ClientSecret) != u.ClientSecret {
			return ErrConfig
		}
		encrypted, e := secrets.Encrypt(s.key, []byte(u.ClientSecret))
		if e != nil {
			return e
		}
		c.Secret = base64.RawStdEncoding.EncodeToString(encrypted)
	}
	if u.Enabled && (s.issuer == "" || len(s.key) != 32 || u.ClientID == "" || c.Secret == "" || len(ids) == 0) {
		return ErrConfig
	}
	c.Enabled = u.Enabled
	c.ClientID = u.ClientID
	c.AllowedIDs = ids
	c.Revision = random()
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, settingKey, string(raw), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Service) enabled() (configuration, error) {
	c, err := s.load()
	if err != nil || !c.Enabled || c.ClientID == "" || c.Secret == "" || len(c.AllowedIDs) == 0 || s.issuer == "" {
		return configuration{}, ErrDenied
	}
	return c, nil
}

type flow struct {
	Form     url.Values `json:"form"`
	Verifier string     `json:"verifier,omitempty"`
	UserID   string     `json:"user_id,omitempty"`
	Login    string     `json:"login,omitempty"`
}

func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hash(v string) string { sum := sha256.Sum256([]byte(v)); return hex.EncodeToString(sum[:]) }

func (s *Service) put(c configuration, browser, phase string, f flow) (string, error) {
	raw, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	encrypted, err := secrets.Encrypt(s.key, raw)
	if err != nil {
		return "", err
	}
	token := random()
	// Bounded lifetime and outstanding-flow count protect the unauthenticated surface.
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	var active int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM settings WHERE key=? AND json_extract(value,'$.revision')=? AND json_extract(value,'$.enabled')=1`, settingKey, c.Revision).Scan(&active); err != nil || active != 1 {
		return "", ErrDenied
	}
	if _, err = tx.Exec(`DELETE FROM operator_login_flows WHERE expires_at<=?`, now); err != nil {
		return "", err
	}
	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM operator_login_flows`).Scan(&count); err != nil || count >= 1000 {
		return "", ErrDenied
	}
	_, err = tx.Exec(`INSERT INTO operator_login_flows(token_hash,browser_hash,revision,phase,payload,expires_at) VALUES(?,?,?,?,?,?)`, hash(token), hash(browser), c.Revision, phase, base64.RawStdEncoding.EncodeToString(encrypted), time.Now().Add(ttl).Unix())
	if err != nil {
		return "", err
	}
	return token, tx.Commit()
}

func (s *Service) take(c configuration, token, browser, phase string) (flow, error) {
	if len(token) != 43 || len(browser) != 43 {
		return flow{}, ErrDenied
	}
	var payload string
	err := s.db.QueryRow(`DELETE FROM operator_login_flows WHERE token_hash=? AND browser_hash=? AND revision=? AND phase=? AND expires_at>? AND EXISTS (SELECT 1 FROM settings WHERE key=? AND json_extract(value,'$.revision')=? AND json_extract(value,'$.enabled')=1) RETURNING payload`, hash(token), hash(browser), c.Revision, phase, time.Now().Unix(), settingKey, c.Revision).Scan(&payload)
	if err != nil {
		return flow{}, ErrDenied
	}
	encrypted, err := base64.RawStdEncoding.DecodeString(payload)
	if err != nil {
		return flow{}, ErrDenied
	}
	raw, err := secrets.Decrypt(s.key, encrypted)
	if err != nil {
		return flow{}, ErrDenied
	}
	var f flow
	if json.Unmarshal(raw, &f) != nil {
		return flow{}, ErrDenied
	}
	return f, nil
}

func (s *Service) Start(w http.ResponseWriter, r *http.Request, form url.Values) error {
	c, err := s.enabled()
	if err != nil {
		return err
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.issuer {
		return ErrDenied
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return ErrDenied
	}
	browser := ""
	if cookie, e := r.Cookie(CookieName); e == nil && len(cookie.Value) == 43 {
		browser = cookie.Value
	}
	if browser == "" {
		browser = random()
	}
	verifier := random()
	state, err := s.put(c, browser, "github", flow{Form: form, Verifier: verifier})
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: browser, Path: "/authorize", HttpOnly: true, Secure: strings.HasPrefix(s.issuer, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())})
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {c.ClientID}, "redirect_uri": {s.issuer + CallbackPath}, "state": {state}, "scope": {""}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "allow_signup": {"false"}, "prompt": {"select_account"}}
	http.Redirect(w, r, "https://github.com/login/oauth/authorize?"+q.Encode(), http.StatusSeeOther)
	return nil
}

func (s *Service) request(ctx context.Context, method, target string, body io.Reader, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return ErrDenied
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "guda-gateway-operator-login")
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return ErrDenied
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrDenied
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return ErrDenied
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrDenied
	}
	return nil
}

// Finish consumes GitHub state before contacting GitHub; it never issues MCP grants.
func (s *Service) Finish(r *http.Request) (url.Values, string, string, error) {
	c, err := s.enabled()
	if err != nil {
		return nil, "", "", err
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return nil, "", "", ErrDenied
	}
	q := r.URL.Query()
	if len(q["state"]) != 1 {
		return nil, "", "", ErrDenied
	}
	f, err := s.take(c, q.Get("state"), cookie.Value, "github")
	if err != nil {
		return nil, "", "", err
	}
	if q.Get("error") != "" || len(q["code"]) != 1 || len(q.Get("code")) == 0 || len(q.Get("code")) > 1024 {
		return nil, "", "", ErrDenied
	}
	encrypted, err := base64.RawStdEncoding.DecodeString(c.Secret)
	if err != nil {
		return nil, "", "", ErrDenied
	}
	secret, err := secrets.Decrypt(s.key, encrypted)
	if err != nil {
		return nil, "", "", ErrDenied
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	values := url.Values{"client_id": {c.ClientID}, "client_secret": {string(secret)}, "code": {q.Get("code")}, "redirect_uri": {s.issuer + CallbackPath}, "code_verifier": {f.Verifier}}
	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
	}
	if s.request(ctx, "POST", "https://github.com/login/oauth/access_token", strings.NewReader(values.Encode()), "", &result) != nil || result.Error != "" || result.AccessToken == "" || !strings.EqualFold(result.TokenType, "bearer") {
		return nil, "", "", ErrDenied
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if s.request(ctx, "GET", "https://api.github.com/user", nil, result.AccessToken, &user) != nil {
		return nil, "", "", ErrDenied
	}
	// Access tokens are used only for this identity lookup and are never persisted.
	f.UserID = strconv.FormatInt(user.ID, 10)
	f.Login = user.Login
	f.Verifier = ""
	if !allowed(c, f.UserID) || f.Login == "" {
		return nil, "", "", ErrDenied
	}
	// Re-read settings so disabling/rotating during the exchange invalidates this login.
	current, err := s.enabled()
	if err != nil || current.Revision != c.Revision {
		return nil, "", "", ErrDenied
	}
	proof, err := s.put(c, cookie.Value, "consent", f)
	if err != nil {
		return nil, "", "", err
	}
	return f.Form, proof, f.Login, nil
}

func allowed(c configuration, id string) bool {
	for _, v := range c.AllowedIDs {
		if v == id {
			return true
		}
	}
	return false
}

// Consume binds the one-use approval to both the browser and every displayed MCP field.
func (s *Service) Consume(r *http.Request, proof string, form url.Values) error {
	c, err := s.enabled()
	if err != nil {
		return err
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.issuer {
		return ErrDenied
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return ErrDenied
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return ErrDenied
	}
	f, err := s.take(c, proof, cookie.Value, "consent")
	if err != nil {
		return err
	}
	if !allowed(c, f.UserID) || f.Form.Encode() != form.Encode() {
		return ErrDenied
	}
	return nil
}
