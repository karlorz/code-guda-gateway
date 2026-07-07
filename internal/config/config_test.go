package config

import (
	"reflect"
	"testing"
)

func TestLoadTrimsCommaSeparatedKeysAndAppliesDefaults(t *testing.T) {
	env := map[string]string{
		"GUDA_GATEWAY_KEYS":      " dev , prod ",
		"GROK_UPSTREAM_BASE_URL": "https://api.x.ai/v1/",
		"GROK_UPSTREAM_API_KEYS": "grok-a,grok-b",
		"TAVILY_API_KEYS":        "tvly-a",
		"FIRECRAWL_API_KEYS":     "fc-a, fc-b",
	}

	cfg, err := LoadFromLookup(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("LoadFromLookup returned error: %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.TavilyBaseURL != "https://api.tavily.com" {
		t.Fatalf("TavilyBaseURL = %q", cfg.TavilyBaseURL)
	}
	if cfg.FirecrawlBaseURL != "https://api.firecrawl.dev/v2" {
		t.Fatalf("FirecrawlBaseURL = %q", cfg.FirecrawlBaseURL)
	}
	if cfg.GrokBaseURL != "https://api.x.ai/v1" {
		t.Fatalf("GrokBaseURL = %q", cfg.GrokBaseURL)
	}

	wantGateway := []string{"dev", "prod"}
	if !reflect.DeepEqual(cfg.GatewayKeys, wantGateway) {
		t.Fatalf("GatewayKeys = %#v, want %#v", cfg.GatewayKeys, wantGateway)
	}

	wantFirecrawl := []string{"fc-a", "fc-b"}
	if !reflect.DeepEqual(cfg.FirecrawlKeys, wantFirecrawl) {
		t.Fatalf("FirecrawlKeys = %#v, want %#v", cfg.FirecrawlKeys, wantFirecrawl)
	}
}

func TestLoadRequiresGatewayKey(t *testing.T) {
	_, err := LoadFromLookup(func(string) (string, bool) {
		return "", false
	})
	if err == nil {
		t.Fatal("LoadFromLookup returned nil error, want missing gateway key error")
	}
}
