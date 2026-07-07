package config

import (
	"errors"
	"os"
	"strings"
)

const (
	defaultAddr             = ":8080"
	defaultTavilyBaseURL    = "https://api.tavily.com"
	defaultFirecrawlBaseURL = "https://api.firecrawl.dev/v2"
)

type Config struct {
	Addr             string
	GatewayKeys      []string
	GrokBaseURL      string
	GrokKeys         []string
	TavilyBaseURL    string
	TavilyKeys       []string
	FirecrawlBaseURL string
	FirecrawlKeys    []string
}

func Load() (Config, error) {
	return LoadFromLookup(os.LookupEnv)
}

func LoadFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		Addr:             lookupDefault(lookup, "ADDR", defaultAddr),
		GatewayKeys:      splitCSV(lookupValue(lookup, "GUDA_GATEWAY_KEYS")),
		GrokBaseURL:      trimRightSlash(lookupValue(lookup, "GROK_UPSTREAM_BASE_URL")),
		GrokKeys:         splitCSV(lookupValue(lookup, "GROK_UPSTREAM_API_KEYS")),
		TavilyBaseURL:    trimRightSlash(lookupDefault(lookup, "TAVILY_BASE_URL", defaultTavilyBaseURL)),
		TavilyKeys:       splitCSV(lookupValue(lookup, "TAVILY_API_KEYS")),
		FirecrawlBaseURL: trimRightSlash(lookupDefault(lookup, "FIRECRAWL_BASE_URL", defaultFirecrawlBaseURL)),
		FirecrawlKeys:    splitCSV(lookupValue(lookup, "FIRECRAWL_API_KEYS")),
	}
	if len(cfg.GatewayKeys) == 0 {
		return Config{}, errors.New("GUDA_GATEWAY_KEYS is required")
	}
	return cfg, nil
}

func lookupValue(lookup func(string) (string, bool), key string) string {
	value, _ := lookup(key)
	return value
}

func lookupDefault(lookup func(string) (string, bool), key, fallback string) string {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		key := strings.TrimSpace(part)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func trimRightSlash(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}
