package providers

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"code-guda-gateway/internal/cooldown"
	"code-guda-gateway/internal/secrets"
)

// SettingsRepo reads and writes provider_settings (e.g. Grok base URL).
type SettingsRepo struct {
	db *sql.DB
}

// NewSettingsRepo creates a settings repository.
func NewSettingsRepo(db *sql.DB) *SettingsRepo {
	return &SettingsRepo{db: db}
}

// GetBaseURL returns the configured base URL or the provider default if unset.
func (r *SettingsRepo) GetBaseURL(provider string) (string, error) {
	if err := validateProvider(provider); err != nil {
		return "", err
	}
	var baseURL string
	err := r.db.QueryRow(
		`SELECT base_url FROM provider_settings WHERE provider = ?`, provider,
	).Scan(&baseURL)
	if err == sql.ErrNoRows {
		return defaultBaseURL(provider), nil
	}
	if err != nil {
		return "", fmt.Errorf("get provider_settings: %w", err)
	}
	return baseURL, nil
}

// SetBaseURL persists the base URL for a provider (creation default only).
// The URL is validated with NormalizeBaseURL (no userinfo/query/fragment).
func (r *SettingsRepo) SetBaseURL(provider, baseURL string) error {
	if err := validateProvider(provider); err != nil {
		return err
	}
	if baseURL == "" {
		return fmt.Errorf("set base url: empty url")
	}
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = r.db.Exec(`
		INSERT INTO provider_settings (provider, base_url, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(provider) DO UPDATE SET base_url = excluded.base_url, updated_at = excluded.updated_at`,
		provider, normalized, now,
	)
	if err != nil {
		return fmt.Errorf("upsert provider_settings: %w", err)
	}
	return nil
}

const (
	settingCooldownRateLimit  = "cooldown_rate_limit_seconds"
	settingCooldownTransient  = "cooldown_transient_seconds"
	settingCooldownCredential = "cooldown_credential_seconds"
	settingMaxRetries         = "max_retries"
	settingProxyDebugAttempts = "proxy_debug_attempts"
)

// GetCooldownSettings loads service-wide cooldown and retry limits from the settings table.
func (r *SettingsRepo) GetCooldownSettings() (cooldown.Settings, error) {
	def := cooldown.DefaultSettings()
	rate, err := r.getSettingDuration(settingCooldownRateLimit, def.RateLimit)
	if err != nil {
		return cooldown.Settings{}, err
	}
	trans, err := r.getSettingDuration(settingCooldownTransient, def.Transient)
	if err != nil {
		return cooldown.Settings{}, err
	}
	cred, err := r.getSettingDuration(settingCooldownCredential, def.Credential)
	if err != nil {
		return cooldown.Settings{}, err
	}
	maxR, err := r.getSettingInt(settingMaxRetries, def.MaxRetries)
	if err != nil {
		return cooldown.Settings{}, err
	}
	return cooldown.Settings{
		RateLimit:  rate,
		Transient:  trans,
		Credential: cred,
		MaxRetries: maxR,
	}, nil
}

// SetCooldownSetting persists one cooldown/retry setting by key.
func (r *SettingsRepo) SetCooldownSetting(key string, value string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(`
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now,
	)
	if err != nil {
		return fmt.Errorf("upsert settings: %w", err)
	}
	return nil
}

func (r *SettingsRepo) getSettingDuration(key string, fallback time.Duration) (time.Duration, error) {
	val, err := r.getSettingString(key, "")
	if err != nil {
		return 0, err
	}
	if val == "" {
		return fallback, nil
	}
	secs, err := strconv.ParseInt(val, 10, 64)
	if err != nil || secs < 0 {
		return fallback, nil
	}
	return time.Duration(secs) * time.Second, nil
}

func (r *SettingsRepo) getSettingInt(key string, fallback int) (int, error) {
	val, err := r.getSettingString(key, "")
	if err != nil {
		return 0, err
	}
	if val == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 1 {
		return fallback, nil
	}
	return n, nil
}

func (r *SettingsRepo) getSettingString(key string, fallback string) (string, error) {
	var val string
	err := r.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %s: %w", key, err)
	}
	return val, nil
}

func (r *SettingsRepo) GetGrokQuotaMode() (string, error) {
	return r.getSettingString("grok_quota_mode", "unsupported")
}

func (r *SettingsRepo) SetGrokQuotaMode(mode string) error {
	if mode != "unsupported" && mode != "grok2api_admin" {
		return fmt.Errorf("invalid grok quota mode: %s", mode)
	}
	return r.SetCooldownSetting("grok_quota_mode", mode)
}

func (r *SettingsRepo) GetGrok2APIAdminBaseURL() (string, error) {
	return r.getSettingString("grok2api_admin_base_url", "")
}

func (r *SettingsRepo) SetGrok2APIAdminBaseURL(url string) error {
	return r.SetCooldownSetting("grok2api_admin_base_url", url)
}

func (r *SettingsRepo) GetGrok2APIAdminKey(masterKey []byte) (string, error) {
	val, err := r.getSettingString("grok2api_admin_key_encrypted", "")
	if err != nil || val == "" {
		return "", err
	}
	enc, err := hex.DecodeString(val)
	if err != nil {
		return "", fmt.Errorf("decode admin key: %w", err)
	}
	plain, err := secrets.Decrypt(masterKey, enc)
	if err != nil {
		return "", fmt.Errorf("decrypt admin key: %w", err)
	}
	return string(plain), nil
}

func (r *SettingsRepo) SetGrok2APIAdminKey(masterKey []byte, key string) error {
	if key == "" {
		return r.SetCooldownSetting("grok2api_admin_key_encrypted", "")
	}
	enc, err := secrets.Encrypt(masterKey, []byte(key))
	if err != nil {
		return fmt.Errorf("encrypt admin key: %w", err)
	}
	val := hex.EncodeToString(enc)
	return r.SetCooldownSetting("grok2api_admin_key_encrypted", val)
}

func (r *SettingsRepo) GetProxyDebugAttempts() (bool, error) {
	val, err := r.getSettingString(settingProxyDebugAttempts, "false")
	if err != nil {
		return false, err
	}
	enabled, err := strconv.ParseBool(val)
	if err != nil {
		return false, nil
	}
	return enabled, nil
}

func (r *SettingsRepo) SetProxyDebugAttempts(enabled bool) error {
	return r.SetCooldownSetting(settingProxyDebugAttempts, strconv.FormatBool(enabled))
}
