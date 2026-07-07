package adminauth

import (
	"net/http"
)

// MiddlewareConfig configures path exclusions for session middleware.
type MiddlewareConfig struct {
	// ExcludePaths are exact paths that skip session checks (e.g. login).
	ExcludePaths map[string]struct{}
}

// Middleware returns an http.Handler that requires a valid admin session cookie,
// except for paths listed in cfg.ExcludePaths.
//
// Local dev over plain HTTP: browsers will not send Secure cookies; tests assert
// Set-Cookie attributes via httptest without relying on browser cookie jars.
func (s *Service) Middleware(cfg MiddlewareConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := cfg.ExcludePaths[r.URL.Path]; ok {
			next.ServeHTTP(w, r)
			return
		}
		sid := SessionIDFromRequest(r)
		valid, err := s.ValidateSession(sid)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !valid {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}