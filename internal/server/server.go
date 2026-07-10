package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"code-guda-gateway/internal/adminauth"
	"code-guda-gateway/internal/adminweb"
	"code-guda-gateway/internal/audit"
	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/providers"
	"code-guda-gateway/internal/proxy"
	"code-guda-gateway/internal/usage"
)

type Server struct {
	proxy        *proxy.Proxy
	gatewayKeys  *gatewaykeys.Service
	providerKeys *providers.KeyRepo
	usage        *usage.UsageRepo
	admin        http.Handler
}

// New builds the HTTP handler. Runtime routes require a valid DB-backed gateway key via gatewayKeys.
func New(cfg config.Config, gatewayKeys *gatewaykeys.Service, db *sql.DB, masterKey []byte) http.Handler {
	keyRepo := providers.NewKeyRepo(db, masterKey)
	settingsRepo := providers.NewSettingsRepo(db)
	keyQuotaRepo := providers.NewKeyQuotaRepo(db)
	attemptLogRepo := proxy.NewAttemptLogRepo(db, proxy.DefaultAttemptLogRetention)
	if cfg.ProxyDebugAttempts != nil {
		if err := settingsRepo.SetProxyDebugAttempts(*cfg.ProxyDebugAttempts); err != nil {
			log.Printf("failed to apply GUDA_PROXY_DEBUG_ATTEMPTS: %v", err)
		}
	}
	attemptRecorder := proxy.NewSettingsAttemptRecorder(settingsRepo, attemptLogRepo)
	px := proxy.New(proxy.Options{AttemptRecorder: attemptRecorder})
	if cs, err := settingsRepo.GetCooldownSettings(); err != nil {
		log.Printf("failed to load cooldown settings from DB, using defaults: %v", err)
	} else {
		px.SetCooldownSettings(cs)
	}
	auth := adminauth.NewServiceWithOptions(db, 24*time.Hour, adminauth.Options{CookieSecure: cfg.AdminCookieSecure})
	quotaRepo := providers.NewQuotaRepo(db)
	adminH := adminweb.New(adminweb.Deps{
		Auth:         auth,
		GatewayKeys:  gatewayKeys,
		ProviderKeys: keyRepo,
		Settings:     settingsRepo,
		Audit:        audit.NewAuditRepo(db),
		Usage:        usage.NewUsageRepo(db),
		Quotas:       quotaRepo,
		KeyQuotas:    keyQuotaRepo,
		AttemptLogs:  attemptLogRepo,
		QuotaRefresher: &providers.QuotaRefresher{
			ProviderKeys: keyRepo,
			Settings:     settingsRepo,
			Quotas:       quotaRepo,
			KeyQuotas:    keyQuotaRepo,
			MasterKey:    masterKey,
		},
	})
	return &Server{
		proxy:        px,
		gatewayKeys:  gatewayKeys,
		providerKeys: keyRepo,
		usage:        usage.NewUsageRepo(db),
		admin:        adminH,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/admin") {
		s.admin.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		s.handleHealth(w, r)
		return
	}
	gwKey, serverErr := s.authorized(r)
	if serverErr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if gwKey == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/grok/v1/models":
		s.forward(w, r, gwKey, providers.ProviderGrok, "/models")
	case r.Method == http.MethodPost && r.URL.Path == "/grok/v1/chat/completions":
		s.forward(w, r, gwKey, providers.ProviderGrok, "/chat/completions")
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/tavily/"):
		s.forward(w, r, gwKey, providers.ProviderTavily, strings.TrimPrefix(r.URL.Path, "/tavily"))
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/firecrawl/"):
		s.forward(w, r, gwKey, providers.ProviderFirecrawl, strings.TrimPrefix(r.URL.Path, "/firecrawl"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) authorized(r *http.Request) (*gatewaykeys.DisplayKey, error) {
	if s.gatewayKeys == nil {
		return nil, nil
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return nil, nil
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	rec, err := s.gatewayKeys.Verify(token)
	if err != nil {
		if errors.Is(err, gatewaykeys.ErrNotAuthorized) {
			return nil, nil
		}
		return nil, err
	}
	return rec, nil
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request, gwKey *gatewaykeys.DisplayKey, provider, path string) {
	// Upstream base URL is selected per endpoint row (SelectEndpoint); provider_settings
	// defaults no longer drive runtime routing.
	res := s.proxy.Forward(w, r, proxy.Target{
		Path:     path,
		Provider: provider,
		Keys:     s.providerKeys,
	})
	s.recordUsage(gwKey, provider, r.URL.Path, res)
}

func (s *Server) recordUsage(gwKey *gatewaykeys.DisplayKey, provider, path string, res proxy.Result) {
	if s.usage == nil || gwKey == nil {
		return
	}
	var statusClass string
	if res.NetworkError {
		statusClass = usage.StatusClassFromNetworkError()
	} else if res.StatusCode != 0 {
		statusClass = usage.StatusClassFromHTTP(res.StatusCode)
	} else {
		return
	}
	keyID := gwKey.ID
	routeFamily := usage.RouteFamilyFromPath(path)
	inc := usage.UsageIncrement{
		Day:          usage.DayUTC(time.Now()),
		GatewayKeyID: &keyID,
		Provider:     provider,
		RouteFamily:  routeFamily,
		StatusClass:  statusClass,
	}
	if err := s.usage.Increment(inc); err != nil {
		log.Printf("usage increment failed: provider=%s route=%s class=%s err=%v",
			provider, routeFamily, statusClass, err)
	}
}
