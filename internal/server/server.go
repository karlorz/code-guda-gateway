package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"code-guda-gateway/internal/config"
	"code-guda-gateway/internal/keypool"
	"code-guda-gateway/internal/proxy"
)

type Server struct {
	cfg           config.Config
	proxy         *proxy.Proxy
	acceptedKeys  map[string]bool
	grokKeys      *keypool.Pool
	tavilyKeys    *keypool.Pool
	firecrawlKeys *keypool.Pool
}

func New(cfg config.Config) http.Handler {
	accepted := make(map[string]bool, len(cfg.GatewayKeys))
	for _, key := range cfg.GatewayKeys {
		accepted[key] = true
	}
	return &Server{
		cfg:           cfg,
		proxy:         proxy.New(proxy.Options{}),
		acceptedKeys:  accepted,
		grokKeys:      keypool.New(cfg.GrokKeys),
		tavilyKeys:    keypool.New(cfg.TavilyKeys),
		firecrawlKeys: keypool.New(cfg.FirecrawlKeys),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		s.handleHealth(w, r)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/grok/v1/models":
		s.forward(w, r, s.cfg.GrokBaseURL, "/models", s.grokKeys)
	case r.Method == http.MethodPost && r.URL.Path == "/grok/v1/chat/completions":
		s.forward(w, r, s.cfg.GrokBaseURL, "/chat/completions", s.grokKeys)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/tavily/"):
		s.forward(w, r, s.cfg.TavilyBaseURL, strings.TrimPrefix(r.URL.Path, "/tavily"), s.tavilyKeys)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/firecrawl/"):
		s.forward(w, r, s.cfg.FirecrawlBaseURL, strings.TrimPrefix(r.URL.Path, "/firecrawl"), s.firecrawlKeys)
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

func (s *Server) authorized(r *http.Request) bool {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	return s.acceptedKeys[token]
}

func (s *Server) forward(w http.ResponseWriter, r *http.Request, baseURL, path string, keys *keypool.Pool) {
	s.proxy.Forward(w, r, proxy.Target{
		BaseURL: baseURL,
		Path:    path,
		Keys:    keys,
	})
}
