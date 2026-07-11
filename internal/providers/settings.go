package providers

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	settingDisplayTimezone    = "display_timezone"
)

// DisplayTimezone is the effective admin display zone.
type DisplayTimezone struct {
	Timezone string `json:"timezone"`
	Source   string `json:"source"` // "stored" | "host"
}

// hostTimezoneName returns a real IANA timezone name for the host default.
// Never returns the bare string "Local" (which breaks JS Intl.DateTimeFormat).
func hostTimezoneName() string {
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" && tz != "Local" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if name, ok := timezoneFromLocaltimeLink("/etc/localtime"); ok {
		return name
	}
	if name := time.Local.String(); name != "" && name != "Local" {
		if _, err := time.LoadLocation(name); err == nil {
			return name
		}
	}
	return "UTC"
}

// timezoneFromLocaltimeLink resolves an IANA name from a zoneinfo symlink target
// (e.g. /var/db/timezone/zoneinfo/Asia/Seoul or .../zoneinfo/America/New_York).
func timezoneFromLocaltimeLink(localtimePath string) (string, bool) {
	target, err := filepath.EvalSymlinks(localtimePath)
	if err != nil {
		// Fall back to raw readlink for relative targets if EvalSymlinks fails.
		target, err = os.Readlink(localtimePath)
		if err != nil {
			return "", false
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(localtimePath), target)
		}
		target = filepath.Clean(target)
	}
	// Normalize separators and look for zoneinfo path segment.
	slash := filepath.ToSlash(target)
	const marker = "/zoneinfo/"
	idx := strings.LastIndex(slash, marker)
	if idx < 0 {
		return "", false
	}
	name := strings.TrimSpace(slash[idx+len(marker):])
	if name == "" || name == "Local" {
		return "", false
	}
	if _, err := time.LoadLocation(name); err != nil {
		return "", false
	}
	return name, true
}

// ValidateIANATimezone checks that tz is a non-empty IANA timezone name.
func ValidateIANATimezone(tz string) error {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return fmt.Errorf("timezone required")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return nil
}

// GetDisplayTimezone returns the stored display timezone, or the host default.
func (r *SettingsRepo) GetDisplayTimezone() (DisplayTimezone, error) {
	val, err := r.getSettingString(settingDisplayTimezone, "")
	if err != nil {
		return DisplayTimezone{}, err
	}
	if strings.TrimSpace(val) == "" {
		return DisplayTimezone{Timezone: hostTimezoneName(), Source: "host"}, nil
	}
	if err := ValidateIANATimezone(val); err != nil {
		// Corrupt stored value: fall back to host rather than break admin UI.
		return DisplayTimezone{Timezone: hostTimezoneName(), Source: "host"}, nil
	}
	return DisplayTimezone{Timezone: strings.TrimSpace(val), Source: "stored"}, nil
}

// SetDisplayTimezone stores an IANA timezone for admin display. Empty string clears
// the stored value so the host default applies. Invalid IANA names do not mutate DB.
func (r *SettingsRepo) SetDisplayTimezone(tz string) error {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		// Clear stored value so host default applies.
		_, err := r.db.Exec(`DELETE FROM settings WHERE key = ?`, settingDisplayTimezone)
		if err != nil {
			return fmt.Errorf("clear display_timezone: %w", err)
		}
		return nil
	}
	if err := ValidateIANATimezone(tz); err != nil {
		return err
	}
	return r.SetCooldownSetting(settingDisplayTimezone, tz)
}

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
