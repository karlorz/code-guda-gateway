package adminweb_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/store"
)

func setupTestAppWithSession(t *testing.T) (http.Handler, *store.Store, *gatewaykeys.Service, string, *http.Cookie) {
	t.Helper()
	app, auth, gk, _, st, _ := openAdminApp(t)
	token := initToken(t, auth)
	cookie := loginSession(t, app, token)
	csrf := csrfForTest(t, app, cookie)
	return app, st, gk, csrf, cookie
}

func TestAdminAPI_PublicIssuance(t *testing.T) {
	app, _, _, csrf, c := setupTestAppWithSession(t)

	// 1. GET default public_issuance -> "off"
	req := httptest.NewRequest(http.MethodGet, "/admin/api/public-issuance", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/public-issuance: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&res)
	if res["value"] != "off" {
		t.Fatalf("default value = %q, want 'off'", res["value"])
	}

	// 2. PATCH without CSRF -> 403
	recNoCSRF := serveMutatingAdmin(app, http.MethodPatch, "/admin/api/public-issuance", `{"value":"open"}`, "", c)
	if recNoCSRF.Code != http.StatusForbidden {
		t.Fatalf("PATCH without CSRF: status=%d want 403", recNoCSRF.Code)
	}

	// 3. PATCH with invalid value -> 400
	recInvalid := serveMutatingAdmin(app, http.MethodPatch, "/admin/api/public-issuance", `{"value":"invalid_mode"}`, csrf, c)
	if recInvalid.Code != http.StatusBadRequest {
		t.Fatalf("PATCH invalid value: status=%d want 400", recInvalid.Code)
	}

	// 4. PATCH with "open" -> 200
	recOpen := serveMutatingAdmin(app, http.MethodPatch, "/admin/api/public-issuance", `{"value":"open"}`, csrf, c)
	if recOpen.Code != http.StatusOK {
		t.Fatalf("PATCH open: status=%d body=%s", recOpen.Code, recOpen.Body.String())
	}

	// 5. GET confirms "open"
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/admin/api/public-issuance", nil)
	req2.AddCookie(c)
	app.ServeHTTP(rec2, req2)
	_ = json.NewDecoder(rec2.Body).Decode(&res)
	if res["value"] != "open" {
		t.Fatalf("value after patch = %q, want 'open'", res["value"])
	}

	// 6. PATCH with "ref_code" -> 200
	recRef := serveMutatingAdmin(app, http.MethodPatch, "/admin/api/public-issuance", `{"value":"ref_code"}`, csrf, c)
	if recRef.Code != http.StatusOK {
		t.Fatalf("PATCH ref_code: status=%d body=%s", recRef.Code, recRef.Body.String())
	}
}

func TestAdminAPI_InviteCodes(t *testing.T) {
	app, _, _, csrf, c := setupTestAppWithSession(t)

	// 1. GET invite-codes initially empty
	req := httptest.NewRequest(http.MethodGet, "/admin/api/invite-codes", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/invite-codes status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 2. POST create invite code
	exp := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	createBody := `{"agent_label_bind":"my-claude","max_redemptions":5,"expires_at":"` + exp + `"}`
	recCreate := serveMutatingAdmin(app, http.MethodPost, "/admin/api/invite-codes", createBody, csrf, c)
	if recCreate.Code != http.StatusOK {
		t.Fatalf("POST /admin/api/invite-codes status=%d body=%s", recCreate.Code, recCreate.Body.String())
	}
	var created gatewaykeys.InviteCode
	if err := json.NewDecoder(recCreate.Body).Decode(&created); err != nil {
		t.Fatalf("decode created invite: %v", err)
	}
	if !strings.HasPrefix(created.Code, "inv_") || created.AgentLabelBind != "my-claude" || created.MaxRedemptions != 5 {
		t.Fatalf("unexpected created invite: %+v", created)
	}

	// 3. GET list includes newly created code
	recList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/admin/api/invite-codes", nil)
	reqList.AddCookie(c)
	app.ServeHTTP(recList, reqList)
	var listResp struct {
		Items []gatewaykeys.InviteCode `json:"items"`
	}
	if err := json.NewDecoder(recList.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].ID != created.ID {
		t.Fatalf("unexpected list: %+v", listResp)
	}

	// 4. POST revoke invite code
	recRevoke := serveMutatingAdmin(app, http.MethodPost, "/admin/api/invite-codes/"+strconv.FormatInt(created.ID, 10)+"/revoke", "", csrf, c)
	if recRevoke.Code != http.StatusOK {
		t.Fatalf("POST revoke invite status=%d body=%s", recRevoke.Code, recRevoke.Body.String())
	}

	// 5. GET confirms revoked_at set
	recList2 := httptest.NewRecorder()
	reqList2 := httptest.NewRequest(http.MethodGet, "/admin/api/invite-codes", nil)
	reqList2.AddCookie(c)
	app.ServeHTTP(recList2, reqList2)
	_ = json.NewDecoder(recList2.Body).Decode(&listResp)
	if listResp.Items[0].RevokedAt == nil {
		t.Fatal("revoked_at is nil after revoke")
	}
}

func TestAdminAPI_GatewayKeys_RevokeUnused(t *testing.T) {
	app, st, _, csrf, c := setupTestAppWithSession(t)

	// Create an old key directly in DB
	oldTime := time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339Nano)
	_, err := st.DB().Exec(`
		INSERT INTO gateway_keys (name, key_prefix, fingerprint, key_hash, enabled, created_at, agent_label, issued_via)
		VALUES ('abandoned-key', 'gsk_old', 'fp1', 'h1', 1, ?, 'abandoned-key', 'operator')`, oldTime)
	if err != nil {
		t.Fatalf("insert old key: %v", err)
	}

	// Also create a fresh key via API
	recCreate := serveMutatingAdmin(app, http.MethodPost, "/admin/api/gateway-keys", `{"name":"fresh-key"}`, csrf, c)
	if recCreate.Code != http.StatusOK {
		t.Fatalf("create fresh key: %d", recCreate.Code)
	}

	// Call revoke-unused
	recRevoke := serveMutatingAdmin(app, http.MethodPost, "/admin/api/gateway-keys/revoke-unused", `{"days":30}`, csrf, c)
	if recRevoke.Code != http.StatusOK {
		t.Fatalf("POST /admin/api/gateway-keys/revoke-unused status=%d body=%s", recRevoke.Code, recRevoke.Body.String())
	}
	var revokeResult struct {
		Status       string `json:"status"`
		RevokedCount int64  `json:"revoked_count"`
	}
	_ = json.NewDecoder(recRevoke.Body).Decode(&revokeResult)
	if revokeResult.RevokedCount != 1 {
		t.Fatalf("revoked_count = %d, want 1", revokeResult.RevokedCount)
	}

	// Check gateway key list JSON fields: agent_label, issued_via, last_used_at, revoked_at
	recList := httptest.NewRecorder()
	reqList := httptest.NewRequest(http.MethodGet, "/admin/api/gateway-keys", nil)
	reqList.AddCookie(c)
	app.ServeHTTP(recList, reqList)
	if recList.Code != http.StatusOK {
		t.Fatalf("GET /admin/api/gateway-keys status=%d", recList.Code)
	}

	var keysResp struct {
		Items []gatewaykeys.DisplayKey `json:"items"`
	}
	if err := json.NewDecoder(recList.Body).Decode(&keysResp); err != nil {
		t.Fatalf("decode keys: %v", err)
	}
	if len(keysResp.Items) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keysResp.Items))
	}
	for _, k := range keysResp.Items {
		if k.IssuedVia == "" {
			t.Errorf("key %s missing issued_via", k.Name)
		}
		if k.AgentLabel == "" {
			t.Errorf("key %s missing agent_label", k.Name)
		}
		if k.Name == "abandoned-key" && k.RevokedAt == nil {
			t.Errorf("abandoned-key should have revoked_at set")
		}
	}
}
