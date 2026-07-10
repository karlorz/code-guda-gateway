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
// The base URL is normalized before insert.
func (r *KeyRepo) AddEndpoint(provider, name, baseURL, rawKey string) (DisplayProviderKey, error) {
	if err := validateProvider(provider); err != nil {
		return DisplayProviderKey{}, err
	}
	if name == "" || rawKey == "" {
		return DisplayProviderKey{}, fmt.Errorf("add provider key: name and raw key required")
	}
	normalized, err := NormalizeBaseURL(baseURL)
	if err != nil {
		return DisplayProviderKey{}, fmt.Errorf("add provider endpoint: %w", err)
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
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.Exec(`
		INSERT INTO provider_keys (
			provider, name, base_url, encrypted_key, key_prefix, fingerprint, enabled,
			consecutive_failures, total_failures, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 1, 0, 0, ?, ?)`,
		provider, name, normalized, enc, prefix, fp, now, now,
	)
	if err != nil {
		return DisplayProviderKey{}, fmt.Errorf("insert provider_keys: %w", err)
	}
	id, _ := res.LastInsertId()
	return DisplayProviderKey{
		ID:          id,
		Provider:    provider,
		Name:        name,
		BaseURL:     normalized,
		KeyPrefix:   prefix,
		Fingerprint: fp,
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
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
			consecutive_failures, total_failures, created_at, updated_at
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
			consecutive_failures, total_failures, created_at, updated_at
		FROM provider_keys WHERE id = ?`, id)
	d, err := scanDisplayKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return DisplayProviderKey{}, fmt.Errorf("provider key %d: not found", id)
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
		return fmt.Errorf("provider key %d: not found", id)
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
		return fmt.Errorf("provider key %d: not found", id)
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
		return fmt.Errorf("provider key %d: not found", id)
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
		return fmt.Errorf("provider key %d: not found", id)
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
		return fmt.Errorf("provider key %d: not found", id)
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
		return SelectedEndpoint{}, fmt.Errorf("provider key %d: not found", id)
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
		return fmt.Errorf("provider key %d: not found", id)
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
		return fmt.Errorf("provider key %d: not found", id)
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
	if err := row.Scan(
		&d.ID, &d.Provider, &d.Name, &d.BaseURL, &d.KeyPrefix, &d.Fingerprint, &enabled,
		&cooldownUntil, &cooldownReason, &lastFailedAt, &lastUsed, &lastSuccess,
		&lastError, &lastStatus, &lastMsg,
		&archivedAt, &lastEventAt, &lastEventSource, &lastEventStatusClass,
		&lastEventHTTPStatus, &lastEventMsg,
		&d.ConsecutiveFailures, &d.TotalFailures, &d.CreatedAt, &d.UpdatedAt,
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
	return d, nil
}

func nullStrPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}
