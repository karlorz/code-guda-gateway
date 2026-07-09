package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	defaultAddr          = "127.0.0.1:8080"
	defaultDBPath        = "/var/lib/code-guda-gateway/gateway.db"
	defaultMasterKeyPath = "/etc/code-guda-gateway/master.key"
)

// Config holds bootstrap-only process settings (env or bootstrap.env).
// Gateway keys and provider credentials live in SQLite, not here.
type Config struct {
	Addr               string
	DBPath             string
	MasterKeyPath      string
	AdminCookieSecure  bool
	ProxyDebugAttempts *bool
}

func Load() (Config, error) {
	return LoadFromLookup(os.LookupEnv)
}

func LoadFromLookup(lookup func(string) (string, bool)) (Config, error) {
	adminCookieSecure := true
	if v, ok := lookup("GUDA_ADMIN_COOKIE_SECURE"); ok && strings.TrimSpace(v) != "" {
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return Config{}, fmt.Errorf("GUDA_ADMIN_COOKIE_SECURE: %w", err)
		}
		adminCookieSecure = parsed
	}
	var proxyDebugAttempts *bool
	if v, ok := lookup("GUDA_PROXY_DEBUG_ATTEMPTS"); ok && strings.TrimSpace(v) != "" {
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return Config{}, fmt.Errorf("GUDA_PROXY_DEBUG_ATTEMPTS: %w", err)
		}
		proxyDebugAttempts = &parsed
	}
	cfg := Config{
		Addr:               lookupDefault(lookup, "ADDR", defaultAddr),
		DBPath:             lookupDefault(lookup, "DB_PATH", defaultDBPath),
		MasterKeyPath:      lookupDefault(lookup, "GUDA_MASTER_KEY_PATH", defaultMasterKeyPath),
		AdminCookieSecure:  adminCookieSecure,
		ProxyDebugAttempts: proxyDebugAttempts,
	}
	return cfg, nil
}

func lookupDefault(lookup func(string) (string, bool), key, fallback string) string {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
