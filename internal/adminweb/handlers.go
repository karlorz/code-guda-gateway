package adminweb

import (
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code-guda-gateway/internal/adminauth"
	"code-guda-gateway/internal/audit"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/usage"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Deps wires admin UI to backing services.
type Deps struct {
	Auth         *adminauth.Service
	GatewayKeys  *gatewaykeys.Service
	ProviderKeys *providers.KeyRepo
	Settings     *providers.SettingsRepo
	Audit        *audit.AuditRepo
	Usage        *usage.UsageRepo
	Quotas         *providers.QuotaRepo
	QuotaRefresher *providers.QuotaRefresher
}

// Handler serves /admin pages and /admin/api/* JSON.
type Handler struct {
	deps      Deps
	templates *template.Template
	static    http.Handler
	api       http.Handler
}

// New returns an http.Handler mounting admin routes.
func New(d Deps) http.Handler {
	tpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		panic("adminweb templates: " + err.Error())
	}
	sub, _ := fs.Sub(staticFS, "static")
	h := &Handler{
		deps:      d,
		templates: tpl,
		static:    http.FileServer(http.FS(sub)),
	}
	exclude := map[string]struct{}{
		"/admin/api/login": {},
	}
	h.api = d.Auth.Middleware(adminauth.MiddlewareConfig{ExcludePaths: exclude}, http.HandlerFunc(h.serveAPI))
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/admin/static" || strings.HasPrefix(path, "/admin/static/") {
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(path, "/admin/static")
		if r2.URL.Path == "" || r2.URL.Path == "/" {
			r2.URL.Path = "/admin.css"
		}
		h.static.ServeHTTP(w, r2)
		return
	}
	if strings.HasPrefix(path, "/admin/api/") {
		h.api.ServeHTTP(w, r)
		return
	}
	if path == "/admin" || strings.HasPrefix(path, "/admin/") {
		h.servePage(w, r)
		return
	}
	http.NotFound(w, r)
}

func (h *Handler) servePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sid := adminauth.SessionIDFromRequest(r)
	valid, err := h.deps.Auth.ValidateSession(sid)
	if err != nil {
		if errors.Is(err, adminauth.ErrSessionInvalid) {
			h.renderLogin(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !valid {
		h.renderLogin(w, r)
		return
	}
	serveSPA(w, r)
}

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request) {
	has, err := h.deps.Auth.HasToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.templates.ExecuteTemplate(w, "login", map[string]any{
		"TokenNotInitialized": !has,
	})
}

type providerRow struct {
	Name          string
	BaseURL       string
	KeyCount      int
	CooldownCount int
}

func (h *Handler) renderDashboard(w http.ResponseWriter, r *http.Request) {
	data, err := h.buildDashboardData()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = h.templates.ExecuteTemplate(w, "dashboard", data)
}

func (h *Handler) buildDashboardData() (map[string]any, error) {
	keys, err := h.deps.GatewayKeys.List()
	if err != nil {
		return nil, err
	}
	pkeys, err := h.deps.ProviderKeys.ListAll()
	if err != nil {
		return nil, err
	}
	events, err := h.deps.Audit.List(audit.ListFilter{})
	if err != nil {
		return nil, err
	}
	if len(events) > 20 {
		events = events[len(events)-20:]
	}
	usageRows, err := h.deps.Usage.ListDaily(usage.ListFilter{})
	if err != nil {
		return nil, err
	}
	var provRows []providerRow
	for _, name := range []string{providers.ProviderGrok, providers.ProviderTavily, providers.ProviderFirecrawl} {
		base, err := h.deps.Settings.GetBaseURL(name)
		if err != nil {
			return nil, err
		}
		list, err := h.deps.ProviderKeys.List(name)
		if err != nil {
			return nil, err
		}
		cd := 0
		now := time.Now().UTC()
		for _, k := range list {
			if k.CooldownUntil != nil {
				if t, e := time.Parse(time.RFC3339Nano, *k.CooldownUntil); e == nil && t.After(now) {
					cd++
				}
			}
		}
		provRows = append(provRows, providerRow{Name: name, BaseURL: base, KeyCount: len(list), CooldownCount: cd})
	}
	return map[string]any{
		"Providers":    provRows,
		"GatewayKeys":  keys,
		"ProviderKeys": pkeys,
		"AuditEvents":  events,
		"Usage":        usageRows,
	}, nil
}

func (h *Handler) serveAPI(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if requiresCSRF(r) {
		ok, err := h.deps.Auth.ValidateCSRF(adminauth.SessionIDFromRequest(r), r.Header.Get("X-CSRF-Token"))
		if err != nil {
			if errors.Is(err, adminauth.ErrSessionInvalid) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	switch {
	case path == "/admin/api/login" && r.Method == http.MethodPost:
		h.handleLogin(w, r)
	case path == "/admin/api/logout" && r.Method == http.MethodPost:
		h.handleLogout(w, r)
	case path == "/admin/api/session" && r.Method == http.MethodGet:
		h.handleSession(w, r)
	case path == "/admin/api/dashboard" && r.Method == http.MethodGet:
		h.handleDashboardJSON(w, r)
	case path == "/admin/api/gateway-keys" && r.Method == http.MethodGet:
		h.handleGatewayKeysList(w, r)
	case path == "/admin/api/gateway-keys" && r.Method == http.MethodPost:
		h.handleGatewayKeysCreate(w, r)
	case strings.HasPrefix(path, "/admin/api/gateway-keys/") && r.Method == http.MethodPatch:
		h.handleGatewayKeysPatch(w, r)
	case strings.HasPrefix(path, "/admin/api/gateway-keys/") && strings.HasSuffix(path, "/revoke") && r.Method == http.MethodPost:
		h.handleGatewayKeysRevoke(w, r)
	case strings.HasPrefix(path, "/admin/api/gateway-keys/") && r.Method == http.MethodDelete:
		h.handleGatewayKeysDelete(w, r)
	case path == "/admin/api/provider-keys" && r.Method == http.MethodGet:
		h.handleProviderKeysList(w, r)
	case path == "/admin/api/provider-keys" && r.Method == http.MethodPost:
		h.handleProviderKeysCreate(w, r)
	case strings.HasPrefix(path, "/admin/api/provider-keys/") && strings.HasSuffix(path, "/reset-cooldown") && r.Method == http.MethodPost:
		h.handleProviderKeysResetCooldown(w, r)
	case strings.HasPrefix(path, "/admin/api/provider-keys/") && strings.HasSuffix(path, "/archive") && r.Method == http.MethodPost:
		h.handleProviderKeysArchive(w, r)
	case strings.HasPrefix(path, "/admin/api/provider-keys/") && strings.HasSuffix(path, "/restore") && r.Method == http.MethodPost:
		h.handleProviderKeysRestore(w, r)
	case strings.HasPrefix(path, "/admin/api/provider-keys/") && r.Method == http.MethodPatch:
		h.handleProviderKeysPatch(w, r)
	case strings.HasPrefix(path, "/admin/api/provider-keys/") && r.Method == http.MethodDelete:
		h.handleProviderKeysDelete(w, r)
	case path == "/admin/api/providers/grok" && r.Method == http.MethodGet:
		h.handleGrokGet(w, r)
	case path == "/admin/api/providers/grok" && r.Method == http.MethodPatch:
		h.handleGrokPatch(w, r)
	case path == "/admin/api/provider-settings" && r.Method == http.MethodGet:
		h.handleProviderSettingsList(w, r)
	case strings.HasPrefix(path, "/admin/api/provider-settings/") && r.Method == http.MethodPatch:
		h.handleProviderSettingsPatch(w, r)
	case path == "/admin/api/provider-health" && r.Method == http.MethodGet:
		h.handleProviderHealth(w, r)
	case strings.HasPrefix(path, "/admin/api/providers/") && strings.HasSuffix(path, "/test") && r.Method == http.MethodPost:
		h.handleProviderManualTest(w, r)
	case path == "/admin/api/provider-quotas" && r.Method == http.MethodGet:
		h.handleProviderQuotas(w, r)
	case strings.HasPrefix(path, "/admin/api/provider-quotas/") && strings.HasSuffix(path, "/refresh") && r.Method == http.MethodPost:
		h.handleProviderQuotaRefresh(w, r)
	case path == "/admin/api/audit-events" && r.Method == http.MethodGet:
		h.handleAuditList(w, r)
	case path == "/admin/api/usage-daily" && r.Method == http.MethodGet:
		h.handleUsageDaily(w, r)
	default:
		http.NotFound(w, r)
	}
}

func requiresCSRF(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPatch, http.MethodDelete:
		return r.URL.Path != "/admin/api/login"
	default:
		return false
	}
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var token string
	if ct := r.Header.Get("Content-Type"); strings.Contains(ct, "application/json") {
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		token = strings.TrimSpace(body.Token)
	} else {
		_ = r.ParseForm()
		token = strings.TrimSpace(r.FormValue("token"))
	}
	res, err := h.deps.Auth.Login(token)
	if err != nil {
		if errors.Is(err, adminauth.ErrInvalidToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, res.Cookie)
	_ = h.deps.Audit.Record(audit.AuditEvent{
		ActorKind: "admin_web",
		Action:    "admin.login",
		Detail:    "result=ok",
		ClientIP:  r.RemoteAddr,
	})
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "csrf_token": res.CSRFToken})
		return
	}
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	sid := adminauth.SessionIDFromRequest(r)
	cookie, err := h.deps.Auth.Logout(sid)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, cookie)
	if wantsHTMLResponse(r) {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func wantsHTMLResponse(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return true
	}
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}
	return strings.Contains(accept, "text/html")
}

func (h *Handler) handleSession(w http.ResponseWriter, r *http.Request) {
	sid := adminauth.SessionIDFromRequest(r)
	valid, err := h.deps.Auth.ValidateSession(sid)
	if err != nil {
		if errors.Is(err, adminauth.ErrSessionInvalid) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !valid {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	csrfToken, err := h.deps.Auth.CSRFToken(sid)
	if err != nil {
		if errors.Is(err, adminauth.ErrSessionInvalid) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "csrf_token": csrfToken})
}

func (h *Handler) handleDashboardJSON(w http.ResponseWriter, r *http.Request) {
	data, err := h.buildDashboardData()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (h *Handler) handleGatewayKeysList(w http.ResponseWriter, r *http.Request) {
	list, err := h.deps.GatewayKeys.List()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	limit, offset := limitOffset(r)
	writeJSON(w, http.StatusOK, listResponse[gatewaykeys.DisplayKey]{Items: list, Page: map[string]int{"limit": limit, "offset": offset}})
}

func (h *Handler) handleGatewayKeysCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "bad request")
		return
	}
	raw, display, err := h.deps.GatewayKeys.Create(body.Name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = h.deps.Audit.Record(audit.AuditEvent{
		ActorKind:  "admin_web",
		Action:     "gateway_key.create",
		TargetKind: "gateway_key",
		TargetID:   strconv.FormatInt(display.ID, 10),
		Detail:     "name=" + body.Name,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"key":     display,
		"raw_key": raw,
	})
}

func (h *Handler) handleGatewayKeysPatch(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDSuffix(r.URL.Path, "/admin/api/gateway-keys/")
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
		Revoke  bool  `json:"revoke"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	switch {
	case body.Revoke:
		err = h.deps.GatewayKeys.Revoke(id)
	case body.Enabled != nil && *body.Enabled:
		err = h.deps.GatewayKeys.Enable(id)
	case body.Enabled != nil && !*body.Enabled:
		err = h.deps.GatewayKeys.Disable(id)
	default:
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleGatewayKeysRevoke(w http.ResponseWriter, r *http.Request) {
	id, err := parseActionID(r.URL.Path, "/admin/api/gateway-keys/", "/revoke")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "bad request")
		return
	}
	if err := h.deps.GatewayKeys.Revoke(id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleGatewayKeysDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDSuffix(r.URL.Path, "/admin/api/gateway-keys/")
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.deps.GatewayKeys.Delete(id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleProviderKeysList(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	var list []providers.DisplayProviderKey
	var err error
	if provider != "" {
		list, err = h.deps.ProviderKeys.List(provider)
	} else {
		list, err = h.deps.ProviderKeys.ListAll()
	}
	if err != nil {
		if errors.Is(err, providers.ErrUnknownProvider) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	limit, offset := limitOffset(r)
	writeJSON(w, http.StatusOK, listResponse[providers.DisplayProviderKey]{Items: list, Page: map[string]int{"limit": limit, "offset": offset}})
}

func (h *Handler) handleProviderKeysCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
		Key      string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	display, err := h.deps.ProviderKeys.Add(body.Provider, body.Name, body.Key)
	if err != nil {
		if errors.Is(err, providers.ErrDuplicateName) || errors.Is(err, providers.ErrUnknownProvider) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = h.deps.Audit.Record(audit.AuditEvent{
		ActorKind:  "admin_web",
		Action:     "provider_key.add",
		TargetKind: "provider_key",
		TargetID:   strconv.FormatInt(display.ID, 10),
		Detail:     "provider=" + body.Provider + ";name=" + body.Name,
	})
	writeJSON(w, http.StatusOK, display)
}

func (h *Handler) handleProviderKeysPatch(w http.ResponseWriter, r *http.Request) {
	id, err := parseProviderKeyID(r.URL.Path)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Enabled == nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if *body.Enabled {
		err = h.deps.ProviderKeys.Enable(id)
	} else {
		err = h.deps.ProviderKeys.Disable(id)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleProviderKeysArchive(w http.ResponseWriter, r *http.Request) {
	h.handleProviderKeyAction(w, r, "/archive", h.deps.ProviderKeys.Archive)
}

func (h *Handler) handleProviderKeysRestore(w http.ResponseWriter, r *http.Request) {
	h.handleProviderKeyAction(w, r, "/restore", h.deps.ProviderKeys.RestoreArchived)
}

func (h *Handler) handleProviderKeysDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseProviderKeyID(r.URL.Path)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.deps.ProviderKeys.Delete(id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleProviderKeysResetCooldown(w http.ResponseWriter, r *http.Request) {
	h.handleProviderKeyAction(w, r, "/reset-cooldown", h.deps.ProviderKeys.ResetCooldown)
}

func (h *Handler) handleProviderKeyAction(w http.ResponseWriter, r *http.Request, suffix string, action func(int64) error) {
	id, err := parseActionID(r.URL.Path, "/admin/api/provider-keys/", suffix)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "bad request")
		return
	}
	if err := action(id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleGrokGet(w http.ResponseWriter, r *http.Request) {
	url, err := h.deps.Settings.GetBaseURL(providers.ProviderGrok)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"base_url": url})
}

func (h *Handler) handleGrokPatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.BaseURL == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.deps.Settings.SetBaseURL(providers.ProviderGrok, body.BaseURL); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"base_url": body.BaseURL})
}

func (h *Handler) handleProviderSettingsList(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
	}
	var items []item
	for _, provider := range adminProviders() {
		baseURL, err := h.deps.Settings.GetBaseURL(provider)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		items = append(items, item{Provider: provider, BaseURL: baseURL})
	}
	writeJSON(w, http.StatusOK, listResponse[item]{Items: items})
}

func (h *Handler) handleProviderSettingsPatch(w http.ResponseWriter, r *http.Request) {
	provider := strings.TrimPrefix(r.URL.Path, "/admin/api/provider-settings/")
	var body struct {
		BaseURL string `json:"base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.BaseURL == "" {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "bad request")
		return
	}
	if err := h.deps.Settings.SetBaseURL(provider, body.BaseURL); err != nil {
		if errors.Is(err, providers.ErrUnknownProvider) {
			writeAPIError(w, http.StatusBadRequest, "bad_request", "unknown provider")
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"provider": provider, "base_url": body.BaseURL})
}

func (h *Handler) handleProviderHealth(w http.ResponseWriter, r *http.Request) {
	items, err := providers.BuildHealth(h.deps.Settings, h.deps.ProviderKeys)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, listResponse[providers.HealthItem]{Items: items})
}

func (h *Handler) handleProviderManualTest(w http.ResponseWriter, r *http.Request) {
	provider, err := providerFromActionPath(r.URL.Path, "/admin/api/providers/", "/test")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "bad request")
		return
	}
	id, _, err := h.deps.ProviderKeys.SelectKey(provider)
	if err != nil {
		if errors.Is(err, providers.ErrNoEnabledKey) {
			writeJSON(w, http.StatusOK, map[string]string{"provider": provider, "status": "missing_key"})
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_ = h.deps.ProviderKeys.MarkLastEvent(id, providers.LastEvent{Source: "manual_test", StatusClass: "2xx", Message: "manual test selected key"})
	writeJSON(w, http.StatusOK, map[string]string{"provider": provider, "status": "ok"})
}

func (h *Handler) handleProviderQuotas(w http.ResponseWriter, r *http.Request) {
	items, err := h.providerQuotaItems()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, listResponse[providers.QuotaCache]{Items: items})
}

func (h *Handler) handleProviderQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	provider, err := providerFromActionPath(r.URL.Path, "/admin/api/provider-quotas/", "/refresh")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "bad_request", "bad request")
		return
	}
	var q providers.QuotaCache
	if h.deps.QuotaRefresher == nil {
		q = unsupportedQuota(provider)
	} else {
		var err error
		q, err = h.deps.QuotaRefresher.Refresh(r.Context(), provider)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	if h.deps.Quotas != nil {
		if err := h.deps.Quotas.Upsert(q); err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, q)
}

func (h *Handler) providerQuotaItems() ([]providers.QuotaCache, error) {
	cached := map[string]providers.QuotaCache{}
	if h.deps.Quotas != nil {
		rows, err := h.deps.Quotas.List()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			cached[row.Provider] = row
		}
	}
	var items []providers.QuotaCache
	for _, provider := range adminProviders() {
		q, ok := cached[provider]
		if !ok {
			q = unsupportedQuota(provider)
		}
		items = append(items, q)
	}
	return items, nil
}

func unsupportedQuota(provider string) providers.QuotaCache {
	now := time.Now().UTC()
	msg := "upstream quota not available"
	return providers.QuotaCache{
		Provider:        provider,
		Source:          "unsupported",
		Available:       false,
		CheckedAt:       now.Format(time.RFC3339Nano),
		ExpiresAt:       now.Add(5 * time.Minute).Format(time.RFC3339Nano),
		MessageRedacted: &msg,
	}
}

func (h *Handler) handleAuditList(w http.ResponseWriter, r *http.Request) {
	limit, offset := limitOffset(r)
	rows, err := h.deps.Audit.List(audit.ListFilter{Action: r.URL.Query().Get("action"), ActorKind: r.URL.Query().Get("actor_kind"), Limit: limit, Offset: offset})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, listResponse[audit.StoredAuditEvent]{Items: rows, Page: map[string]int{"limit": limit, "offset": offset}})
}

func (h *Handler) handleUsageDaily(w http.ResponseWriter, r *http.Request) {
	limit, offset := limitOffset(r)
	q := r.URL.Query()
	rows, err := h.deps.Usage.ListDaily(usage.ListFilter{
		Day:         q.Get("day"),
		From:        q.Get("from"),
		To:          q.Get("to"),
		Provider:    q.Get("provider"),
		RouteFamily: q.Get("route_family"),
		StatusClass: q.Get("status_class"),
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, listResponse[usage.UsageDaily]{Items: rows, Page: map[string]int{"limit": limit, "offset": offset}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseIDSuffix(path, prefix string) (int64, error) {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, "/")
	return strconv.ParseInt(s, 10, 64)
}

func parseProviderKeyID(path string) (int64, error) {
	path = strings.TrimPrefix(path, "/admin/api/provider-keys/")
	path = strings.TrimSuffix(path, "/")
	if i := strings.Index(path, "/"); i >= 0 {
		path = path[:i]
	}
	return strconv.ParseInt(path, 10, 64)
}

func parseActionID(path, prefix, suffix string) (int64, error) {
	path = strings.TrimPrefix(path, prefix)
	path = strings.TrimSuffix(path, suffix)
	path = strings.TrimSuffix(path, "/")
	return strconv.ParseInt(path, 10, 64)
}

func providerFromActionPath(path, prefix, suffix string) (string, error) {
	provider := strings.TrimPrefix(path, prefix)
	provider = strings.TrimSuffix(provider, suffix)
	provider = strings.Trim(provider, "/")
	switch provider {
	case providers.ProviderGrok, providers.ProviderTavily, providers.ProviderFirecrawl:
		return provider, nil
	default:
		return "", providers.ErrUnknownProvider
	}
}

func adminProviders() []string {
	return []string{providers.ProviderGrok, providers.ProviderTavily, providers.ProviderFirecrawl}
}

func limitOffset(r *http.Request) (int, int) {
	limit := 100
	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v >= 0 {
		offset = v
	}
	return limit, offset
}

// RenderDashboardHTML exposes dashboard HTML for tests (session must be valid on request).
func (h *Handler) RenderDashboardHTML(w http.ResponseWriter, r *http.Request) error {
	data, err := h.buildDashboardData()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return h.templates.ExecuteTemplate(w, "dashboard", data)
}
