package providers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"code-guda-gateway/internal/secrets"
)

const keyPrefixLen = 6
const redactMaxLen = 256

// DisplayProviderKey is the public view of a provider key (no raw or ciphertext).
type DisplayProviderKey struct {
	ID                       int64
	Provider                 string
	Name                     string
	BaseURL                  string
	KeyPrefix                string
	Fingerprint              string
	Enabled                  bool
	CooldownUntil            *string
	CooldownReason           *string
	LastFailedAt             *string
	LastUsedAt               *string
	LastSuccessAt            *string
	LastErrorAt              *string
	LastErrorStatus          *int
	LastErrorMessageRedacted *string
	ArchivedAt               *string
	LastEventAt              *string
	LastEventSource          *string
	LastEventStatusClass     *string
	LastEventHTTPStatus      *int
	LastEventMessageRedacted *string
	ConsecutiveFailures      int
	TotalFailures            int
	CreatedAt                string
	UpdatedAt                string
	// Safe quota sidecar metadata (never ciphertext or raw keys).
	QuotaMode           QuotaMode
	QuotaFlow           QuotaFlow
	QuotaBaseURL        *string
	QuotaKeyConfigured  bool
	QuotaKeyPrefix      *string
	QuotaKeyFingerprint *string
}

// ProviderEndpoint is a compatibility alias for the display row of an atomic
// provider endpoint (base URL + key metadata, no raw secret).
type ProviderEndpoint = DisplayProviderKey

// SelectedEndpoint is a decrypted atomic (base URL, API key) pair selected for
// an upstream request.
type SelectedEndpoint struct {
	ID       int64
	Provider string
	Name     string
	BaseURL  string
	APIKey   string
}

// KeyRepo manages encrypted upstream provider keys in SQLite.
type KeyRepo struct {
	db        *sql.DB
	masterKey []byte
}

// NewKeyRepo creates a provider key repository.
func NewKeyRepo(db *sql.DB, masterKey []byte) *KeyRepo {
	return &KeyRepo{db: db, masterKey: masterKey}
}

// Add stores an encrypted provider key, snapshotting the provider's configured
// (or compiled default) base URL into the row so each key is an atomic endpoint.
// name must be unique per provider.
// Duplicate (provider, name) is rejected with ErrDuplicateName via check-before-insert
// (enforced in DB by migration 0003 unique index idx_provider_keys_provider_name).
func (r *KeyRepo) Add(provider, name, rawKey string) (DisplayProviderKey, error) {
	baseURL, err := NewSettingsRepo(r.db).GetBaseURL(provider)
	if err != nil {
		return DisplayProviderKey{}, err
	}
	return r.AddEndpoint(provider, name, baseURL, rawKey)
}

// AddEndpoint stores an encrypted provider key with an explicit endpoint base URL.
// Applies provider DefaultQuotaConfig and delegates to AddEndpointWithQuota.
func (r *KeyRepo) AddEndpoint(provider, name, baseURL, rawKey string) (DisplayProviderKey, error) {
	mode, flow, err := DefaultQuotaConfig(provider)
	if err != nil {
		return DisplayProviderKey{}, err
	}
	return r.AddEndpointWithQuota(provider, name, baseURL, rawKey, EndpointQuotaInput{
		Mode: mode,
		Flow: flow,
	})
}

// AddEndpointWithQuota stores an encrypted provider endpoint with quota sidecar
// configuration. Separate quota keys are encrypted independently of the inference key.
func (r *KeyRepo) AddEndpointWithQuota(provider, name, baseURL, rawKey string, quota EndpointQuotaInput) (DisplayProviderKey, error) {
	if err := validateProvider(provider); err != nil {
		return DisplayProviderKey{}, err
	}
	if name == "" || rawKey == "" {
		return DisplayProviderKey{}, fmt.Errorf("add provider key: name and raw key required")
	}
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return DisplayProviderKey{}, err
	}
	q, err := ValidateQuotaConfig(provider, quota, true)
	if err != nil {
		return DisplayProviderKey{}, err
	}
	var existing int
	if err := r.db.QueryRow(
		`SELECT COUNT(*) FROM provider_keys WHERE provider = ? AND name = ?`, provider, name,
	).Scan(&existing); err != nil {
		return DisplayProviderKey{}, fmt.Errorf("check provider_keys name: %w", err)
	}
	if existing > 0 {
		return DisplayProviderKey{}, ErrDuplicateName
	}
	prefix := keyPrefix(rawKey)
	fp := fingerprint(rawKey)
	enc, err := secrets.Encrypt(r.masterKey, []byte(rawKey))
	if err != nil {
		return DisplayProviderKey{}, err
	}

	var quotaBase interface{}
	var encQuota interface{}
	var qPrefix, qFP interface{}
	if q.Mode == QuotaSeparateCredentials {
		quotaBase = q.BaseURL
		encQ, err := secrets.Encrypt(r.masterKey, []byte(q.RawKey))
		if err != nil {
			return DisplayProviderKey{}, err
		}
		encQuota = encQ
		qp := keyPrefix(q.RawKey)
		qf := fingerprint(q.RawKey)
		qPrefix, qFP = qp, qf
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`
		INSERT INTO provider_keys (
			provider, name, base_url, encrypted_key, key_prefix, fingerprint, enabled,
			consecutive_failures, total_failures, created_at, updated_at,
			quota_mode, quota_flow, quota_base_url, encrypted_quota_key,
			quota_key_prefix, quota_key_fingerprint
		) VALUES (?, ?, ?, ?, ?, ?, 1, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?)`,
		provider, name, normalized, enc, prefix, fp, now, now,
		string(q.Mode), string(q.Flow), quotaBase, encQuota, qPrefix, qFP,
	)
	if err != nil {
		return DisplayProviderKey{}, fmt.Errorf("insert provider_keys: %w", err)
	}
	id, _ := res.LastInsertId()
	d := DisplayProviderKey{
		ID:          id,
		Provider:    provider,
		Name:        name,
		BaseURL:     normalized,
		KeyPrefix:   prefix,
		Fingerprint: fp,
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
		QuotaMode:   q.Mode,
		QuotaFlow:   q.Flow,
	}
	if q.Mode == QuotaSeparateCredentials {
		u := q.BaseURL
		d.QuotaBaseURL = &u
		d.QuotaKeyConfigured = true
		if qp, ok := qPrefix.(string); ok {
			d.QuotaKeyPrefix = &qp
		}
		if qf, ok := qFP.(string); ok {
			d.QuotaKeyFingerprint = &qf
		}
	}
	return d, nil
}

// UpdateEndpointQuota updates mode/flow/URL metadata without accepting a raw key.
// Leaving separate mode NULLs quota_base_url, encrypted_quota_key, and identity
// columns in the same SQL update. Inference routing state is preserved.
func (r *KeyRepo) UpdateEndpointQuota(id int64, input EndpointQuotaInput) error {
	input.RawKey = ""
	var provider string
	err := r.db.QueryRow(
		`SELECT provider FROM provider_keys WHERE id = ?`, id,
	).Scan(&provider)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("load endpoint for quota update: %w", err)
	}
	q, err := ValidateQuotaConfig(provider, input, false)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	switch q.Mode {
	case QuotaDisabled, QuotaEndpointCredentials:
		res, err := r.db.Exec(`
			UPDATE provider_keys SET
				quota_mode = ?,
				quota_flow = ?,
				quota_base_url = NULL,
				encrypted_quota_key = NULL,
				quota_key_prefix = NULL,
				quota_key_fingerprint = NULL,
				updated_at = ?
			WHERE id = ?`,
			string(q.Mode), string(q.Flow), now, id,
		)
		if err != nil {
			return fmt.Errorf("update endpoint quota: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
		}
		return nil

	case QuotaSeparateCredentials:
		res, err := r.db.Exec(`
			UPDATE provider_keys SET
				quota_mode = ?,
				quota_flow = ?,
				quota_base_url = ?,
				updated_at = ?
			WHERE id = ?`,
			string(q.Mode), string(q.Flow), q.BaseURL, now, id,
		)
		if err != nil {
			return fmt.Errorf("update endpoint quota: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown quota mode %q", ErrInvalidQuotaConfig, q.Mode)
	}
}

// RotateEndpointQuotaKey replaces the separate encrypted quota key for an endpoint
// that is in separate_credentials mode. Inference key and routing state are untouched.
func (r *KeyRepo) RotateEndpointQuotaKey(id int64, rawKey string) error {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return fmt.Errorf("rotate quota key: raw key required")
	}
	var modeStr string
	err := r.db.QueryRow(`SELECT quota_mode FROM provider_keys WHERE id = ?`, id).Scan(&modeStr)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("load endpoint for quota rotate: %w", err)
	}
	if QuotaMode(modeStr) != QuotaSeparateCredentials {
		return fmt.Errorf("%w: rotate quota key requires separate_credentials mode", ErrInvalidQuotaConfig)
	}
	prefix := keyPrefix(rawKey)
	fp := fingerprint(rawKey)
	enc, err := secrets.Encrypt(r.masterKey, []byte(rawKey))
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`
		UPDATE provider_keys SET
			encrypted_quota_key = ?,
			quota_key_prefix = ?,
			quota_key_fingerprint = ?,
			updated_at = ?
		WHERE id = ?`,
		enc, prefix, fp, now, id,
	)
	if err != nil {
		return fmt.Errorf("rotate quota key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	return nil
}

// ResolveEndpointQuota loads the owning row once and returns decrypted quota
// credentials. It does not update last_used_at.
func (r *KeyRepo) ResolveEndpointQuota(id int64) (ResolvedEndpointQuota, error) {
	var provider, modeStr, flowStr, baseURL string
	var encKey []byte
	var quotaBase sql.NullString
	var encQuota []byte
	err := r.db.QueryRow(`
		SELECT provider, base_url, encrypted_key, quota_mode, quota_flow,
			quota_base_url, encrypted_quota_key
		FROM provider_keys WHERE id = ?`, id,
	).Scan(&provider, &baseURL, &encKey, &modeStr, &flowStr, &quotaBase, &encQuota)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolvedEndpointQuota{}, fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	if err != nil {
		return ResolvedEndpointQuota{}, fmt.Errorf("resolve endpoint quota: %w", err)
	}
	mode := QuotaMode(modeStr)
	flow := QuotaFlow(flowStr)
	switch mode {
	case QuotaDisabled:
		return ResolvedEndpointQuota{}, ErrQuotaDisabled
	case QuotaEndpointCredentials:
		plain, err := secrets.Decrypt(r.masterKey, encKey)
		if err != nil {
			return ResolvedEndpointQuota{}, fmt.Errorf("decrypt provider key: %w", err)
		}
		return ResolvedEndpointQuota{
			EndpointID: id,
			Provider:   provider,
			Mode:       mode,
			Flow:       flow,
			BaseURL:    baseURL,
			APIKey:     string(plain),
		}, nil
	case QuotaSeparateCredentials:
		if len(encQuota) == 0 {
			return ResolvedEndpointQuota{}, ErrQuotaNotConfigured
		}
		if !quotaBase.Valid || quotaBase.String == "" {
			return ResolvedEndpointQuota{}, ErrQuotaNotConfigured
		}
		plain, err := secrets.Decrypt(r.masterKey, encQuota)
		if err != nil {
			return ResolvedEndpointQuota{}, fmt.Errorf("decrypt quota key: %w", err)
		}
		return ResolvedEndpointQuota{
			EndpointID: id,
			Provider:   provider,
			Mode:       mode,
			Flow:       flow,
			BaseURL:    quotaBase.String,
			APIKey:     string(plain),
		}, nil
	default:
		return ResolvedEndpointQuota{}, fmt.Errorf("%w: unknown quota mode %q", ErrInvalidQuotaConfig, mode)
	}
}

// List returns display rows for all keys of a provider.
func (r *KeyRepo) List(provider string) ([]DisplayProviderKey, error) {
	if err := validateProvider(provider); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(`
		SELECT id, provider, name, base_url, key_prefix, fingerprint, enabled,
			cooldown_until, cooldown_reason, last_failed_at, last_used_at, last_success_at,
			last_error_at, last_error_status, last_error_message_redacted,
			archived_at, last_event_at, last_event_source, last_event_status_class,
			last_event_http_status, last_event_message_redacted,
			consecutive_failures, total_failures, created_at, updated_at,
			quota_mode, quota_flow, quota_base_url, quota_key_prefix, quota_key_fingerprint,
			CASE WHEN encrypted_quota_key IS NOT NULL AND length(encrypted_quota_key) > 0 THEN 1 ELSE 0 END
		FROM provider_keys WHERE provider = ? ORDER BY id`, provider)
	if err != nil {
		return nil, fmt.Errorf("list provider_keys: %w", err)
	}
	defer rows.Close()
	var out []DisplayProviderKey
	for rows.Next() {
		d, err := scanDisplayKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Get returns one key by id (display fields only).
func (r *KeyRepo) Get(id int64) (DisplayProviderKey, error) {
	row := r.db.QueryRow(`
		SELECT id, provider, name, base_url, key_prefix, fingerprint, enabled,
			cooldown_until, cooldown_reason, last_failed_at, last_used_at, last_success_at,
			last_error_at, last_error_status, last_error_message_redacted,
			archived_at, last_event_at, last_event_source, last_event_status_class,
			last_event_http_status, last_event_message_redacted,
			consecutive_failures, total_failures, created_at, updated_at,
			quota_mode, quota_flow, quota_base_url, quota_key_prefix, quota_key_fingerprint,
			CASE WHEN encrypted_quota_key IS NOT NULL AND length(encrypted_quota_key) > 0 THEN 1 ELSE 0 END
		FROM provider_keys WHERE id = ?`, id)
	d, err := scanDisplayKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DisplayProviderKey{}, fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	return d, err
}

// Disable sets enabled=false.
func (r *KeyRepo) Disable(id int64) error {
	return r.setEnabled(id, false)
}

// Enable sets enabled=true.
func (r *KeyRepo) Enable(id int64) error {
	return r.setEnabled(id, true)
}

// ResetCooldown clears cooldown and failure-demotion order for a provider key.
func (r *KeyRepo) ResetCooldown(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`
		UPDATE provider_keys SET
			cooldown_until = NULL,
			cooldown_reason = NULL,
			last_failed_at = NULL,
			updated_at = ?
		WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("reset cooldown: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	return nil
}

// ResetSelection clears only last_failed_at so the key re-enters the never-failed pack.
// Cooldown is left unchanged.
func (r *KeyRepo) ResetSelection(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`
		UPDATE provider_keys SET last_failed_at = NULL, updated_at = ? WHERE id = ?`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("reset selection: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	return nil
}

// DemoteToEnd marks the key as most-recently-failed so SelectKey ranks it last
// among non-cooled keys. Does not set cooldown.
func (r *KeyRepo) DemoteToEnd(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`
		UPDATE provider_keys SET last_failed_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id,
	)
	if err != nil {
		return fmt.Errorf("demote provider key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	return nil
}

// Archive disables a provider key and removes it from default selection.
func (r *KeyRepo) Archive(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`UPDATE provider_keys SET enabled = 0, archived_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	if err != nil {
		return fmt.Errorf("archive provider key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	return nil
}

// RestoreArchived restores an archived provider key as disabled.
func (r *KeyRepo) RestoreArchived(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`UPDATE provider_keys SET enabled = 0, archived_at = NULL, updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return fmt.Errorf("restore provider key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	return nil
}

// ListAll returns display rows for every provider key across all providers.
func (r *KeyRepo) ListAll() ([]DisplayProviderKey, error) {
	var out []DisplayProviderKey
	for _, p := range []string{ProviderGrok, ProviderTavily, ProviderFirecrawl} {
		rows, err := r.List(p)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// Delete removes a provider key row.
func (r *KeyRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM provider_keys WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete provider_keys: %w", err)
	}
	return nil
}

// SelectKey picks an enabled key not in cooldown, decrypts it, and updates last_used_at.
// Compatibility wrapper over SelectEndpoint (returns id + raw key only).
// Selection policy (sticky winner + failure demotion):
//
//	ORDER BY last_failed_at ASC NULLS FIRST, id ASC
//
// Never-failed keys (NULL last_failed_at) sort first; among demoted keys the oldest
// failure is tried first. Cooldown still excludes keys until cooldown_until.
func (r *KeyRepo) SelectKey(provider string) (keyID int64, rawKey string, err error) {
	endpoint, err := r.SelectEndpoint(provider)
	return endpoint.ID, endpoint.APIKey, err
}

// SelectEndpoint picks an enabled endpoint not in cooldown, returns the atomic
// (base URL, API key) pair from the same row, and updates last_used_at.
func (r *KeyRepo) SelectEndpoint(provider string) (SelectedEndpoint, error) {
	if err := validateProvider(provider); err != nil {
		return SelectedEndpoint{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var id int64
	var name, baseURL string
	var enc []byte
	err := r.db.QueryRow(`
		SELECT id, provider, name, base_url, encrypted_key FROM provider_keys
		WHERE provider = ? AND enabled = 1
		  AND (cooldown_until IS NULL OR cooldown_until < ?)
		  AND archived_at IS NULL
		ORDER BY last_failed_at IS NOT NULL, last_failed_at ASC, id ASC
		LIMIT 1`,
		provider, now,
	).Scan(&id, &provider, &name, &baseURL, &enc)
	if errors.Is(err, sql.ErrNoRows) {
		return SelectedEndpoint{}, ErrNoEnabledKey
	}
	if err != nil {
		return SelectedEndpoint{}, fmt.Errorf("select provider endpoint: %w", err)
	}
	plain, err := secrets.Decrypt(r.masterKey, enc)
	if err != nil {
		return SelectedEndpoint{}, fmt.Errorf("decrypt provider key: %w", err)
	}
	if _, err := r.db.Exec(
		`UPDATE provider_keys SET last_used_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id,
	); err != nil {
		return SelectedEndpoint{}, fmt.Errorf("update last_used_at: %w", err)
	}
	return SelectedEndpoint{
		ID:       id,
		Provider: provider,
		Name:     name,
		BaseURL:  baseURL,
		APIKey:   string(plain),
	}, nil
}

// RawKey decrypts the stored key for a specific row id without updating last_used_at.
// Used by admin per-key quota refresh so selection stats are not perturbed.
func (r *KeyRepo) RawKey(id int64) (string, error) {
	ep, err := r.RawEndpoint(id)
	if err != nil {
		return "", err
	}
	return ep.APIKey, nil
}

// RawEndpoint decrypts the stored key and returns the atomic endpoint pair for a
// specific row id without updating last_used_at.
func (r *KeyRepo) RawEndpoint(id int64) (SelectedEndpoint, error) {
	var provider, name, baseURL string
	var enc []byte
	err := r.db.QueryRow(
		`SELECT id, provider, name, base_url, encrypted_key FROM provider_keys WHERE id = ?`, id,
	).Scan(&id, &provider, &name, &baseURL, &enc)
	if errors.Is(err, sql.ErrNoRows) {
		return SelectedEndpoint{}, fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	if err != nil {
		return SelectedEndpoint{}, fmt.Errorf("load provider key: %w", err)
	}
	plain, err := secrets.Decrypt(r.masterKey, enc)
	if err != nil {
		return SelectedEndpoint{}, fmt.Errorf("decrypt provider key: %w", err)
	}
	return SelectedEndpoint{
		ID:       id,
		Provider: provider,
		Name:     name,
		BaseURL:  baseURL,
		APIKey:   string(plain),
	}, nil
}

// UpdateBaseURL sets a normalized endpoint base URL and clears cooldown/demotion
// so the endpoint can re-enter the selection pack. Counters and ID are preserved.
func (r *KeyRepo) UpdateBaseURL(id int64, baseURL string) error {
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return fmt.Errorf("update base url: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`
		UPDATE provider_keys SET
			base_url = ?,
			cooldown_until = NULL,
			cooldown_reason = NULL,
			last_failed_at = NULL,
			updated_at = ?
		WHERE id = ?`,
		normalized, now, id,
	)
	if err != nil {
		return fmt.Errorf("update base url: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	return nil
}

// RotateKey replaces the encrypted key material, updates prefix/fingerprint, and
// clears cooldown/demotion. ID, base URL, and failure counters are preserved.
func (r *KeyRepo) RotateKey(id int64, rawKey string) error {
	if rawKey == "" {
		return fmt.Errorf("rotate key: raw key required")
	}
	prefix := keyPrefix(rawKey)
	fp := fingerprint(rawKey)
	enc, err := secrets.Encrypt(r.masterKey, []byte(rawKey))
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`
		UPDATE provider_keys SET
			encrypted_key = ?,
			key_prefix = ?,
			fingerprint = ?,
			cooldown_until = NULL,
			cooldown_reason = NULL,
			last_failed_at = NULL,
			updated_at = ?
		WHERE id = ?`,
		enc, prefix, fp, now, id,
	)
	if err != nil {
		return fmt.Errorf("rotate key: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("%w: id %d", ErrProviderKeyNotFound, id)
	}
	return nil
}

// LastEvent is the latest control-plane or runtime event summary for a key.
type LastEvent struct {
	Source      string
	StatusClass string
	HTTPStatus  *int
	Message     string
}

// MarkLastEvent stores a redacted latest event summary for a provider key.
func (r *KeyRepo) MarkLastEvent(keyID int64, ev LastEvent) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(`
		UPDATE provider_keys SET last_event_at = ?, last_event_source = ?,
			last_event_status_class = ?, last_event_http_status = ?,
			last_event_message_redacted = ?, updated_at = ?
		WHERE id = ?`,
		now, ev.Source, ev.StatusClass, ev.HTTPStatus, Redact(ev.Message), now, keyID)
	if err != nil {
		return fmt.Errorf("mark last event: %w", err)
	}
	return nil
}

// MarkSuccess records a successful upstream use and clears failure demotion.
func (r *KeyRepo) MarkSuccess(keyID int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(`
		UPDATE provider_keys SET
			last_success_at = ?,
			consecutive_failures = 0,
			last_failed_at = NULL,
			updated_at = ?
		WHERE id = ?`, now, now, keyID)
	if err != nil {
		return fmt.Errorf("mark success: %w", err)
	}
	return nil
}

// MarkFailure records a failed upstream use with a redacted message only.
func (r *KeyRepo) MarkFailure(keyID int64, status int, redactedMsg string) error {
	return r.MarkFailureWithCooldown(keyID, status, redactedMsg, nil, nil)
}

// MarkFailureWithCooldown records failure and optionally sets cooldown_until/reason.
// When a cooldown is applied (until != nil), last_failed_at is also set so the key
// sorts to the end of the selection pack after cool expires.
func (r *KeyRepo) MarkFailureWithCooldown(keyID int64, status int, redactedMsg string, until *time.Time, reason *string) error {
	msg := Redact(redactedMsg)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var untilStr, reasonStr interface{}
	if until != nil {
		untilStr = until.UTC().Format(time.RFC3339Nano)
	}
	if reason != nil {
		reasonStr = *reason
	}
	// Only demote when a cooldown policy is applied (same outcomes as cool policies).
	var lastFailed interface{}
	if until != nil {
		lastFailed = now
	}
	_, err := r.db.Exec(`
		UPDATE provider_keys SET
			last_error_at = ?,
			last_error_status = ?,
			last_error_message_redacted = ?,
			consecutive_failures = consecutive_failures + 1,
			total_failures = total_failures + 1,
			cooldown_until = ?,
			cooldown_reason = ?,
			last_failed_at = COALESCE(?, last_failed_at),
			updated_at = ?
		WHERE id = ?`, now, status, msg, untilStr, reasonStr, lastFailed, now, keyID)
	if err != nil {
		return fmt.Errorf("mark failure: %w", err)
	}
	return nil
}

var (
	reBearer = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._\-]+`)
	reAPIKey = regexp.MustCompile(`(?i)api[_-]?key\s*=\s*[^\s&]+`)
	reSkLike = regexp.MustCompile(`\b(sk|tvly|fc|xai)[-_][A-Za-z0-9]{8,}\b`)
)

// Redact strips key-like material and truncates for safe storage in audit/DB fields.
func Redact(msg string) string {
	if msg == "" {
		return "upstream_error"
	}
	s := reBearer.ReplaceAllString(msg, "Bearer [REDACTED]")
	s = reAPIKey.ReplaceAllString(s, "api_key=[REDACTED]")
	s = reSkLike.ReplaceAllString(s, "[REDACTED]")
	s = strings.TrimSpace(s)
	if s == "" {
		return "upstream_error"
	}
	if len(s) > redactMaxLen {
		return s[:redactMaxLen] + "…"
	}
	return s
}

func (r *KeyRepo) setEnabled(id int64, on bool) error {
	v := 0
	if on {
		v = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(`UPDATE provider_keys SET enabled = ?, updated_at = ? WHERE id = ?`, v, now, id)
	if err != nil {
		return fmt.Errorf("set enabled: %w", err)
	}
	return nil
}

func keyPrefix(raw string) string {
	if len(raw) <= keyPrefixLen {
		return raw
	}
	return raw[:keyPrefixLen]
}

func fingerprint(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])[:6]
}

type displayKeyScanner interface {
	Scan(dest ...any) error
}

func scanDisplayKey(row displayKeyScanner) (DisplayProviderKey, error) {
	var d DisplayProviderKey
	var enabled int
	var cooldownUntil, cooldownReason, lastFailedAt sql.NullString
	var lastUsed, lastSuccess, lastError sql.NullString
	var lastStatus sql.NullInt64
	var lastMsg sql.NullString
	var archivedAt, lastEventAt, lastEventSource, lastEventStatusClass, lastEventMsg sql.NullString
	var lastEventHTTPStatus sql.NullInt64
	var quotaMode, quotaFlow string
	var quotaBaseURL, quotaKeyPrefix, quotaKeyFingerprint sql.NullString
	var quotaKeyConfigured int
	if err := row.Scan(
		&d.ID, &d.Provider, &d.Name, &d.BaseURL, &d.KeyPrefix, &d.Fingerprint, &enabled,
		&cooldownUntil, &cooldownReason, &lastFailedAt, &lastUsed, &lastSuccess,
		&lastError, &lastStatus, &lastMsg,
		&archivedAt, &lastEventAt, &lastEventSource, &lastEventStatusClass,
		&lastEventHTTPStatus, &lastEventMsg,
		&d.ConsecutiveFailures, &d.TotalFailures, &d.CreatedAt, &d.UpdatedAt,
		&quotaMode, &quotaFlow, &quotaBaseURL, &quotaKeyPrefix, &quotaKeyFingerprint,
		&quotaKeyConfigured,
	); err != nil {
		return DisplayProviderKey{}, err
	}
	d.Enabled = enabled != 0
	d.CooldownUntil = nullStrPtr(cooldownUntil)
	d.CooldownReason = nullStrPtr(cooldownReason)
	d.LastFailedAt = nullStrPtr(lastFailedAt)
	d.LastUsedAt = nullStrPtr(lastUsed)
	d.LastSuccessAt = nullStrPtr(lastSuccess)
	d.LastErrorAt = nullStrPtr(lastError)
	d.LastErrorMessageRedacted = nullStrPtr(lastMsg)
	d.ArchivedAt = nullStrPtr(archivedAt)
	d.LastEventAt = nullStrPtr(lastEventAt)
	d.LastEventSource = nullStrPtr(lastEventSource)
	d.LastEventStatusClass = nullStrPtr(lastEventStatusClass)
	d.LastEventMessageRedacted = nullStrPtr(lastEventMsg)
	if lastStatus.Valid {
		v := int(lastStatus.Int64)
		d.LastErrorStatus = &v
	}
	if lastEventHTTPStatus.Valid {
		v := int(lastEventHTTPStatus.Int64)
		d.LastEventHTTPStatus = &v
	}
	d.QuotaMode = QuotaMode(quotaMode)
	d.QuotaFlow = QuotaFlow(quotaFlow)
	d.QuotaBaseURL = nullStrPtr(quotaBaseURL)
	d.QuotaKeyPrefix = nullStrPtr(quotaKeyPrefix)
	d.QuotaKeyFingerprint = nullStrPtr(quotaKeyFingerprint)
	d.QuotaKeyConfigured = quotaKeyConfigured != 0
	return d, nil
}

func nullStrPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}
