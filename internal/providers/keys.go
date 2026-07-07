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
	ID                        int64
	Provider                  string
	Name                      string
	KeyPrefix                 string
	Fingerprint               string
	Enabled                   bool
	CooldownUntil             *string
	CooldownReason            *string
	LastUsedAt                *string
	LastSuccessAt             *string
	LastErrorAt               *string
	LastErrorStatus           *int
	LastErrorMessageRedacted  *string
	ConsecutiveFailures       int
	TotalFailures             int
	CreatedAt                 string
	UpdatedAt                 string
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

// Add stores an encrypted provider key. name must be unique per provider.
// Duplicate (provider, name) is rejected with ErrDuplicateName via check-before-insert
// (enforced in DB by migration 0003 unique index idx_provider_keys_provider_name).
func (r *KeyRepo) Add(provider, name, rawKey string) (DisplayProviderKey, error) {
	if err := validateProvider(provider); err != nil {
		return DisplayProviderKey{}, err
	}
	if name == "" || rawKey == "" {
		return DisplayProviderKey{}, fmt.Errorf("add provider key: name and raw key required")
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
			provider, name, encrypted_key, key_prefix, fingerprint, enabled,
			consecutive_failures, total_failures, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 1, 0, 0, ?, ?)`,
		provider, name, enc, prefix, fp, now, now,
	)
	if err != nil {
		return DisplayProviderKey{}, fmt.Errorf("insert provider_keys: %w", err)
	}
	id, _ := res.LastInsertId()
	return DisplayProviderKey{
		ID:          id,
		Provider:    provider,
		Name:        name,
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
		SELECT id, provider, name, key_prefix, fingerprint, enabled,
			cooldown_until, cooldown_reason, last_used_at, last_success_at,
			last_error_at, last_error_status, last_error_message_redacted,
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
		SELECT id, provider, name, key_prefix, fingerprint, enabled,
			cooldown_until, cooldown_reason, last_used_at, last_success_at,
			last_error_at, last_error_status, last_error_message_redacted,
			consecutive_failures, total_failures, created_at, updated_at
		FROM provider_keys WHERE id = ?`, id)
	d, err := scanDisplayKeyRow(row)
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

// Delete removes a provider key row.
func (r *KeyRepo) Delete(id int64) error {
	_, err := r.db.Exec(`DELETE FROM provider_keys WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete provider_keys: %w", err)
	}
	return nil
}

// SelectKey picks the lowest-id enabled key not in cooldown, decrypts it, and updates last_used_at.
// Selection policy: ORDER BY id ASC LIMIT 1 (documented for Task 6 replacement).
func (r *KeyRepo) SelectKey(provider string) (keyID int64, rawKey string, err error) {
	if err := validateProvider(provider); err != nil {
		return 0, "", err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var id int64
	var enc []byte
	err = r.db.QueryRow(`
		SELECT id, encrypted_key FROM provider_keys
		WHERE provider = ? AND enabled = 1
		  AND (cooldown_until IS NULL OR cooldown_until < ?)
		ORDER BY id ASC LIMIT 1`,
		provider, now,
	).Scan(&id, &enc)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNoEnabledKey
	}
	if err != nil {
		return 0, "", fmt.Errorf("select provider key: %w", err)
	}
	plain, err := secrets.Decrypt(r.masterKey, enc)
	if err != nil {
		return 0, "", fmt.Errorf("decrypt provider key: %w", err)
	}
	if _, err := r.db.Exec(
		`UPDATE provider_keys SET last_used_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id,
	); err != nil {
		return 0, "", fmt.Errorf("update last_used_at: %w", err)
	}
	return id, string(plain), nil
}

// MarkSuccess records a successful upstream use for cooldown tracking (Task 6).
func (r *KeyRepo) MarkSuccess(keyID int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(`
		UPDATE provider_keys SET
			last_success_at = ?,
			consecutive_failures = 0,
			updated_at = ?
		WHERE id = ?`, now, now, keyID)
	if err != nil {
		return fmt.Errorf("mark success: %w", err)
	}
	return nil
}

// MarkFailure records a failed upstream use with a redacted message only.
func (r *KeyRepo) MarkFailure(keyID int64, status int, redactedMsg string) error {
	msg := Redact(redactedMsg)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(`
		UPDATE provider_keys SET
			last_error_at = ?,
			last_error_status = ?,
			last_error_message_redacted = ?,
			consecutive_failures = consecutive_failures + 1,
			total_failures = total_failures + 1,
			updated_at = ?
		WHERE id = ?`, now, status, msg, now, keyID)
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

func scanDisplayKey(rows *sql.Rows) (DisplayProviderKey, error) {
	var d DisplayProviderKey
	var enabled int
	var cooldownUntil, cooldownReason sql.NullString
	var lastUsed, lastSuccess, lastError sql.NullString
	var lastStatus sql.NullInt64
	var lastMsg sql.NullString
	if err := rows.Scan(
		&d.ID, &d.Provider, &d.Name, &d.KeyPrefix, &d.Fingerprint, &enabled,
		&cooldownUntil, &cooldownReason, &lastUsed, &lastSuccess,
		&lastError, &lastStatus, &lastMsg,
		&d.ConsecutiveFailures, &d.TotalFailures, &d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		return DisplayProviderKey{}, err
	}
	d.Enabled = enabled != 0
	d.CooldownUntil = nullStrPtr(cooldownUntil)
	d.CooldownReason = nullStrPtr(cooldownReason)
	d.LastUsedAt = nullStrPtr(lastUsed)
	d.LastSuccessAt = nullStrPtr(lastSuccess)
	d.LastErrorAt = nullStrPtr(lastError)
	d.LastErrorMessageRedacted = nullStrPtr(lastMsg)
	if lastStatus.Valid {
		v := int(lastStatus.Int64)
		d.LastErrorStatus = &v
	}
	return d, nil
}

func scanDisplayKeyRow(row *sql.Row) (DisplayProviderKey, error) {
	var d DisplayProviderKey
	var enabled int
	var cooldownUntil, cooldownReason sql.NullString
	var lastUsed, lastSuccess, lastError sql.NullString
	var lastStatus sql.NullInt64
	var lastMsg sql.NullString
	if err := row.Scan(
		&d.ID, &d.Provider, &d.Name, &d.KeyPrefix, &d.Fingerprint, &enabled,
		&cooldownUntil, &cooldownReason, &lastUsed, &lastSuccess,
		&lastError, &lastStatus, &lastMsg,
		&d.ConsecutiveFailures, &d.TotalFailures, &d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		return DisplayProviderKey{}, err
	}
	d.Enabled = enabled != 0
	d.CooldownUntil = nullStrPtr(cooldownUntil)
	d.CooldownReason = nullStrPtr(cooldownReason)
	d.LastUsedAt = nullStrPtr(lastUsed)
	d.LastSuccessAt = nullStrPtr(lastSuccess)
	d.LastErrorAt = nullStrPtr(lastError)
	d.LastErrorMessageRedacted = nullStrPtr(lastMsg)
	if lastStatus.Valid {
		v := int(lastStatus.Int64)
		d.LastErrorStatus = &v
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