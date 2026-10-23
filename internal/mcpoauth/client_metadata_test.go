package mcpoauth

import (
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/store"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type metadataTransport func(*http.Request) (*http.Response, error)

func (f metadataTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestMetadataAddressBoundary(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "::ffff:127.0.0.1", "fc00::1", "100.64.0.1", "0.0.0.0", "2002:7f00:1::1"} {
		if publicMetadataIP(netip.MustParseAddr(raw)) {
			t.Errorf("accepted nonpublic address %s", raw)
		}
	}
	if !publicMetadataIP(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public address rejected")
	}
	for _, raw := range []string{"http://client.example/meta.json", "https://127.0.0.1/meta.json", "https://user:pass@client.example/meta.json", "https://client.example/", "https://client.example/meta.json#fragment"} {
		if validMetadataURL(raw) {
			t.Errorf("accepted invalid URL %s", raw)
		}
	}
	client := newMetadataClient()
	if err := client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatal("metadata redirects must not be followed")
	}
}

func TestClientMetadataValidation(t *testing.T) {
	id := "https://client.example/oauth.json"
	for _, tc := range []struct {
		name, body, media string
		status            int
		valid             bool
	}{
		{"valid", `{"client_id":"` + id + `","client_name":"Client","redirect_uris":["https://client.example/callback"]}`, "application/json", 200, true},
		{"wrong identity", `{"client_id":"https://other.example/meta","client_name":"Client","redirect_uris":["https://client.example/cb"]}`, "application/json", 200, false},
		{"bad redirect", `{"client_id":"` + id + `","client_name":"Client","redirect_uris":["http://client.example/cb"]}`, "application/json", 200, false},
		{"oversize", strings.Repeat(" ", 65537), "application/json", 200, false},
		{"html", `<html></html>`, "text/html", 200, false},
		{"redirect", `{}`, "application/json", 302, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{metadataClient: &http.Client{Transport: metadataTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": []string{tc.media}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			c, err := s.lookupClient(id)
			if tc.valid && (err != nil || c == nil) {
				t.Fatalf("valid metadata rejected: %v", err)
			}
			if !tc.valid && err == nil && c != nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}

func TestMetadataClientCompletesPKCEWithoutRegistration(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := HashPassword("test-password")
	s := NewServer("https://search.example", hash, st.DB())
	id := "https://client.example/oauth.json"
	s.metadataClient = &http.Client{Transport: metadataTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"client_id":"` + id + `","client_name":"Metadata Client","redirect_uris":["http://127.0.0.1:8000/callback"]}`))}, nil
	})}
	verifier := strings.Repeat("a", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	post := func(path string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	form := url.Values{"client_id": {id}, "redirect_uri": {"http://127.0.0.1:45678/callback"}, "password": {"test-password"}, "code_challenge_method": {"S256"}, "code_challenge": {challenge}, "scope": {"mcp"}}
	w := post("/authorize", form)
	if w.Code != 302 {
		t.Fatalf("CIMD authorize: %d", w.Code)
	}
	callback, _ := url.Parse(w.Header().Get("Location"))
	w = post("/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {id}, "code": {callback.Query().Get("code")}, "redirect_uri": {form.Get("redirect_uri")}, "code_verifier": {verifier}, "resource": {"https://search.example/mcp"}})
	if w.Code != 200 {
		t.Fatalf("CIMD exchange: %d", w.Code)
	}
	var token map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	if token["access_token"] == nil || token["scope"] != "mcp" || token["refresh_token"] != nil {
		t.Fatal("CIMD flow did not honor requested scope")
	}
	var clients int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM oauth_clients`).Scan(&clients); err != nil || clients != 0 {
		t.Fatal("CIMD required a DCR registration")
	}
	form.Set("redirect_uri", "http://127.0.0.1:45678/different-path")
	if post("/authorize", form).Code != 400 {
		t.Fatal("loopback matching ignored callback path")
	}
}

func TestLegacyGrantIssuedBetweenMigrationAndRestart(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, key, err := gatewaykeys.CreateOAuthTx(st.DB(), "legacy-client")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = st.DB().Exec(`INSERT INTO oauth_grants(refresh_hash,gateway_key_id,client_id,access_expires_at,refresh_expires_at,created_at) VALUES(?,?,?,?,?,?)`, "old-hash", key.ID, "old-client", now.Add(time.Hour).Format(time.RFC3339Nano), now.Add(24*time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := HashPassword("test-password")
	if NewServer("https://search.example", hash, st.DB()) == nil {
		t.Fatal("startup failed")
	}
	var resource, family, scope string
	if err := st.DB().QueryRow(`SELECT resource,family_id,scope FROM oauth_grants WHERE gateway_key_id=?`, key.ID).Scan(&resource, &family, &scope); err != nil {
		t.Fatal(err)
	}
	if resource != "https://search.example/mcp" || family == "" || scope != "mcp offline_access" {
		t.Fatal("legacy grant was not reconciled")
	}
	var histories int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM oauth_refresh_history WHERE refresh_hash='old-hash' AND family_id=?`, family).Scan(&histories); err != nil || histories != 1 {
		t.Fatal("legacy refresh history missing")
	}
	// A later issuer change cannot turn an already-bound grant into another service's token.
	if NewServer("https://other.example", hash, st.DB()) == nil {
		t.Fatal("second startup failed")
	}
	if err := st.DB().QueryRow(`SELECT resource FROM oauth_grants WHERE gateway_key_id=?`, key.ID).Scan(&resource); err != nil || resource != "https://search.example/mcp" {
		t.Fatal("existing resource was silently rebound")
	}
}
