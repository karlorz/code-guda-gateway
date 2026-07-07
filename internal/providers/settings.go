package providers

import (
	"database/sql"
	"fmt"
	"time"
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

// SetBaseURL persists the base URL for a provider.
func (r *SettingsRepo) SetBaseURL(provider, baseURL string) error {
	if err := validateProvider(provider); err != nil {
		return err
	}
	if baseURL == "" {
		return fmt.Errorf("set base url: empty url")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(`
		INSERT INTO provider_settings (provider, base_url, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(provider) DO UPDATE SET base_url = excluded.base_url, updated_at = excluded.updated_at`,
		provider, baseURL, now,
	)
	if err != nil {
		return fmt.Errorf("upsert provider_settings: %w", err)
	}
	return nil
}