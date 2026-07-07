package config

import (
	"os"
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
	Addr          string
	DBPath        string
	MasterKeyPath string
}

func Load() (Config, error) {
	return LoadFromLookup(os.LookupEnv)
}

func LoadFromLookup(lookup func(string) (string, bool)) (Config, error) {
	cfg := Config{
		Addr:          lookupDefault(lookup, "ADDR", defaultAddr),
		DBPath:        lookupDefault(lookup, "DB_PATH", defaultDBPath),
		MasterKeyPath: lookupDefault(lookup, "GUDA_MASTER_KEY_PATH", defaultMasterKeyPath),
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
