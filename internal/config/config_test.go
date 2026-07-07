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
