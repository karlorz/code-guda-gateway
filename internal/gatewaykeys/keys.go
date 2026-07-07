package gatewaykeys

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"
)

const (
	keyPrefixLabel = "gsk_"
	keyRandomLen   = 32
)

// Fingerprint is the first 6 hex characters of the SHA-256 hash of the raw key (display only).
func fingerprintFromHash(hashHex string) string {
	if len(hashHex) >= 6 {
		return hashHex[:6]
	}
	return hashHex
}

var (
	base62Alphabet = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")
	rawKeyRe       = regexp.MustCompile(`^gsk_[A-Za-z0-9]{32}$`)

	ErrNotAuthorized = errors.New("gatewaykeys: not authorized")
	ErrNotFound      = errors.New("gatewaykeys: not found")
)

// Service provides gateway key CRUD and verification against SQLite.
type Service struct {
	db *sql.DB
}

// NewService creates a gateway key service.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// DisplayKey is the public view of a gateway key (no raw key or full hash).
type DisplayKey struct {
	ID         int64
	Name       string
	Prefix     string
	Fingerprint string
	Enabled    bool
	CreatedAt  string
	LastUsedAt *string
	RevokedAt  *string
}

// Create generates a new enabled key. raw is returned once; only hash is stored.
func (s *Service) Create(name string) (raw string, display DisplayKey, err error) {
	raw, hash, prefix, fp, err := generateKeyMaterial()
	if err != nil {
		return "", DisplayKey{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(
		`INSERT INTO gateway_keys (name, key_prefix, fingerprint, key_hash, enabled, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		name, prefix, fp, hash, now,
	)
	if err != nil {
		return "", DisplayKey{}, fmt.Errorf("insert gateway_keys: %w", err)
	}
	id, _ := res.LastInsertId()
	return raw, DisplayKey{
		ID:          id,
		Name:        name,
		Prefix:      prefix,
		Fingerprint: fp,
		Enabled:     true,
		CreatedAt:   now,
	}, nil
}

// List returns display fields for all gateway keys.
func (s *Service) List() ([]DisplayKey, error) {
	rows, err := s.db.Query(`
		SELECT id, name, key_prefix, fingerprint, enabled, created_at, last_used_at, revoked_at
		FROM gateway_keys ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list gateway_keys: %w", err)
	}
	defer rows.Close()
	var out []DisplayKey
	for rows.Next() {
		var d DisplayKey
		var enabled int
		var lastUsed, revoked sql.NullString
		if err := rows.Scan(&d.ID, &d.Name, &d.Prefix, &d.Fingerprint, &enabled, &d.CreatedAt, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		d.Enabled = enabled != 0
		if lastUsed.Valid {
			d.LastUsedAt = &lastUsed.String
		}
		if revoked.Valid {
			d.RevokedAt = &revoked.String
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Verify checks a raw bearer key. On success updates last_used_at and returns the key record.
// Returns (nil, nil) for unknown keys; (nil, ErrNotAuthorized) for disabled or revoked.
func (s *Service) Verify(raw string) (*DisplayKey, error) {
	if raw == "" || !rawKeyRe.MatchString(raw) {
		return nil, nil
	}
	hash := hashKey(raw)
	var d DisplayKey
	var enabled int
	var lastUsed, revoked sql.NullString
	err := s.db.QueryRow(`
		SELECT id, name, key_prefix, fingerprint, enabled, created_at, last_used_at, revoked_at
		FROM gateway_keys WHERE key_hash = ?`, hash,
	).Scan(&d.ID, &d.Name, &d.Prefix, &d.Fingerprint, &enabled, &d.CreatedAt, &lastUsed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select gateway_keys: %w", err)
	}
	d.Enabled = enabled != 0
	if lastUsed.Valid {
		d.LastUsedAt = &lastUsed.String
	}
	if revoked.Valid {
		d.RevokedAt = &revoked.String
	}
	if !d.Enabled || d.RevokedAt != nil {
		return nil, ErrNotAuthorized
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE gateway_keys SET last_used_at = ? WHERE id = ?`, now, d.ID); err != nil {
		return nil, fmt.Errorf("update last_used_at: %w", err)
	}
	d.LastUsedAt = &now
	return &d, nil
}

// Disable sets enabled=false.
func (s *Service) Disable(id int64) error {
	_, err := s.db.Exec(`UPDATE gateway_keys SET enabled = 0 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("disable gateway_keys: %w", err)
	}
	return nil
}

// Enable sets enabled=true (does not clear revoked_at; revoked keys stay unusable).
func (s *Service) Enable(id int64) error {
	_, err := s.db.Exec(`UPDATE gateway_keys SET enabled = 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("enable gateway_keys: %w", err)
	}
	return nil
}

// Revoke disables the key and sets revoked_at.
func (s *Service) Revoke(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`UPDATE gateway_keys SET enabled = 0, revoked_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return fmt.Errorf("revoke gateway_keys: %w", err)
	}
	return nil
}

// Delete removes a gateway key row.
func (s *Service) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM gateway_keys WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete gateway_keys: %w", err)
	}
	return nil
}

func generateKeyMaterial() (raw, hashHex, displayPrefix, fingerprint string, err error) {
	suffix, err := randomBase62(keyRandomLen)
	if err != nil {
		return "", "", "", "", err
	}
	raw = keyPrefixLabel + suffix
	hashHex = hashKey(raw)
	displayPrefix = raw
	if len(displayPrefix) > 8 {
		displayPrefix = displayPrefix[:8]
	}
	fingerprint = fingerprintFromHash(hashHex)
	return raw, hashHex, displayPrefix, fingerprint, nil
}

func hashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func randomBase62(n int) (string, error) {
	alphabetLen := len(base62Alphabet)
	const maxByte = 256
	limit := (maxByte / alphabetLen) * alphabetLen
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		for {
			var b [1]byte
			if _, err := rand.Read(b[:]); err != nil {
				return "", fmt.Errorf("rand: %w", err)
			}
			if int(b[0]) < limit {
				out[i] = base62Alphabet[int(b[0])%alphabetLen]
				break
			}
		}
	}
	return string(out), nil
}