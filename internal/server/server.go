package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/gatewaykeys"
	"code-guda-gateway/internal/proxy"
	"code-guda-gateway/internal/providers"
)

type Server struct {
	cfg         config.Config
	proxy       *proxy.Proxy
	gatewayKeys *gatewaykeys.Service
	providerKeys *providers.KeyRepo
	settings    *providers.SettingsRepo
}

// New builds the HTTP handler. Runtime routes require a valid DB-backed gateway key via gatewayKeys.
func New(cfg config.Config, gatewayKeys *gatewaykeys.Service, db *sql.DB, masterKey []byte) http.Handler {
	keyRepo := providers.NewKeyRepo(db, masterKey)
	settingsRepo := providers.NewSettingsRepo(db)
	px := proxy.New(proxy.Options{})
	if cs, err := settingsRepo.GetCooldownSettings(); err != nil {
		log.Printf("failed to load cooldown settings from DB, using defaults: %v", err)
	} else {
		px.SetCooldownSettings(cs)
	}
	return &Server{
		cfg:          cfg,
		proxy:        px,
		gatewayKeys:  gatewayKeys,
		providerKeys: keyRepo,
		settings:     settingsRepo,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		s.handleHealth(w, r)
		return
	}
	ok, serverErr := s.authorized(r)
	if serverErr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/grok/v1/models":
		s.forward(w, r, providers.ProviderGrok, "/models")
	case r.Method == http.MethodPost && r.URL.Path == "/grok/v1/chat/completions":
		s.forward(w, r, providers.ProviderGrok, "/chat/completions")
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/tavily/"):
		s.forward(w, r, providers.ProviderTavily, strings.TrimPrefix(r.URL.Path, "/tavily"))
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/firecrawl/"):
		s.forward(w, r, providers.ProviderFirecrawl, strings.TrimPrefix(r.URL.Path, "/firecrawl"))
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

func (s *Server) authorized(r *http.Request) (ok bool, serverErr error) {
	if s.gatewayKeys == nil {
		return false, nil
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false, nil
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	rec, err := s.gatewayKeys.Verify(token)
	if err != nil {
		if errors.Is(err, gatewaykeys.ErrNotAuthorized) {
			return false, nil
		}
		return false, err
	}
	return rec != nil, nil
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request, provider, path string) {
	baseURL, err := s.settings.GetBaseURL(provider)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if baseURL == "" {
		baseURL = s.cfgFallbackBaseURL(provider)
	}
	s.proxy.Forward(w, r, proxy.Target{
		BaseURL:  baseURL,
		Path:     path,
		Provider: provider,
		Keys:     s.providerKeys,
	})
}

func (s *Server) cfgFallbackBaseURL(provider string) string {
	switch provider {
	case providers.ProviderGrok:
		return s.cfg.GrokBaseURL
	case providers.ProviderTavily:
		if s.cfg.TavilyBaseURL != "" {
			return s.cfg.TavilyBaseURL
		}
		return providers.DefaultTavilyBaseURL
	case providers.ProviderFirecrawl:
		if s.cfg.FirecrawlBaseURL != "" {
			return s.cfg.FirecrawlBaseURL
		}
		return providers.DefaultFirecrawlBaseURL
	default:
		return ""
	}
}