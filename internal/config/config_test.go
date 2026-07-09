package config

import (
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := LoadFromLookup(func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatalf("LoadFromLookup returned error: %v", err)
	}
	if cfg.Addr != "127.0.0.1:8080" {
		t.Fatalf("Addr = %q, want 127.0.0.1:8080", cfg.Addr)
	}
	if cfg.DBPath != "/var/lib/code-guda-gateway/gateway.db" {
		t.Fatalf("DBPath = %q", cfg.DBPath)
	}
	if cfg.MasterKeyPath != "/etc/code-guda-gateway/master.key" {
		t.Fatalf("MasterKeyPath = %q", cfg.MasterKeyPath)
	}
	if !cfg.AdminCookieSecure {
		t.Fatal("AdminCookieSecure default = false, want true")
	}
	if cfg.ProxyDebugAttempts != nil {
		t.Fatal("ProxyDebugAttempts default = non-nil, want nil")
	}
}

func TestLoad_NoGatewayKeysRequired(t *testing.T) {
	_, err := LoadFromLookup(func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatalf("LoadFromLookup must succeed without GUDA_GATEWAY_KEYS: %v", err)
	}
}

func TestLoad_OnlyBootstrapSettings(t *testing.T) {
	env := map[string]string{
		"ADDR":                   " 0.0.0.0:9090 ",
		"DB_PATH":                "/tmp/gw.db",
		"GUDA_MASTER_KEY_PATH":   "/tmp/master.key",
		"GUDA_GATEWAY_KEYS":      "should-not-load",
		"GROK_UPSTREAM_BASE_URL": "https://evil.example/v1",
		"GROK_UPSTREAM_API_KEYS": "grok-a",
		"TAVILY_BASE_URL":        "https://evil.tavily",
		"TAVILY_API_KEYS":        "tvly-a",
		"FIRECRAWL_BASE_URL":     "https://evil.fc",
		"FIRECRAWL_API_KEYS":     "fc-a",
	}
	cfg, err := LoadFromLookup(func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadFromLookup: %v", err)
	}
	if cfg.Addr != "0.0.0.0:9090" {
		t.Fatalf("Addr = %q", cfg.Addr)
	}
	if cfg.DBPath != "/tmp/gw.db" {
		t.Fatalf("DBPath = %q", cfg.DBPath)
	}
	if cfg.MasterKeyPath != "/tmp/master.key" {
		t.Fatalf("MasterKeyPath = %q", cfg.MasterKeyPath)
	}
}

func TestLoad_AdminCookieSecureDefaultAndOverride(t *testing.T) {
	t.Setenv("GUDA_ADMIN_COOKIE_SECURE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load default: %v", err)
	}
	if !cfg.AdminCookieSecure {
		t.Fatal("AdminCookieSecure default = false, want true")
	}
	t.Setenv("GUDA_ADMIN_COOKIE_SECURE", "false")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load override: %v", err)
	}
	if cfg.AdminCookieSecure {
		t.Fatal("AdminCookieSecure override = true, want false")
	}
}

func TestLoad_ProxyDebugAttempts(t *testing.T) {
	t.Parallel()

	// unset -> nil
	cfg, err := LoadFromLookup(func(string) (string, bool) {
		return "", false
	})
	if err != nil {
		t.Fatalf("LoadFromLookup unset: %v", err)
	}
	if cfg.ProxyDebugAttempts != nil {
		t.Fatalf("ProxyDebugAttempts unset = %v, want nil", cfg.ProxyDebugAttempts)
	}

	// empty string -> nil
	cfg, err = LoadFromLookup(func(key string) (string, bool) {
		if key == "GUDA_PROXY_DEBUG_ATTEMPTS" {
			return "  ", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("LoadFromLookup empty: %v", err)
	}
	if cfg.ProxyDebugAttempts != nil {
		t.Fatalf("ProxyDebugAttempts empty = %v, want nil", cfg.ProxyDebugAttempts)
	}

	cases := []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
	}
	for _, tc := range cases {
		tc := tc
		cfg, err := LoadFromLookup(func(key string) (string, bool) {
			if key == "GUDA_PROXY_DEBUG_ATTEMPTS" {
				return tc.value, true
			}
			return "", false
		})
		if err != nil {
			t.Fatalf("LoadFromLookup %q: %v", tc.value, err)
		}
		if cfg.ProxyDebugAttempts == nil {
			t.Fatalf("ProxyDebugAttempts %q = nil, want %v", tc.value, tc.want)
		}
		if *cfg.ProxyDebugAttempts != tc.want {
			t.Fatalf("ProxyDebugAttempts %q = %v, want %v", tc.value, *cfg.ProxyDebugAttempts, tc.want)
		}
	}

	// invalid -> error
	_, err = LoadFromLookup(func(key string) (string, bool) {
		if key == "GUDA_PROXY_DEBUG_ATTEMPTS" {
			return "not-a-bool", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("LoadFromLookup invalid: expected error")
	}
}
