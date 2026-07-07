package adminauth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"code-guda-gateway/internal/idgen"
)

const (
	tokenPrefixLabel = "gat_"
	tokenRandomLen   = 32
)

var (
	rawTokenRe = regexp.MustCompile(`^gat_[A-Za-z0-9]{32}$`)

	ErrNoAdminToken   = errors.New("adminauth: no admin token configured")
	ErrInvalidToken   = errors.New("adminauth: invalid admin token")
	ErrTokenAlreadySet = errors.New("adminauth: admin token already initialized")
)

// Service provides admin token and session operations against SQLite.
type Service struct {
	db         *sql.DB
	sessionTTL time.Duration
}

// NewService creates an auth service. sessionTTL is how long browser sessions last (e.g. 24h).
func NewService(db *sql.DB, sessionTTL time.Duration) *Service {
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	return &Service{db: db, sessionTTL: sessionTTL}
}

// Init creates the sole admin token row. The raw token is returned once and never stored.
func (s *Service) Init() (raw string, err error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_tokens`).Scan(&n); err != nil {
		return "", fmt.Errorf("count admin_tokens: %w", err)
	}
	if n > 0 {
		return "", ErrTokenAlreadySet
	}
	raw, hash, prefix, err := generateTokenMaterial()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO admin_tokens (token_hash, key_prefix, created_at) VALUES (?, ?, ?)`,
		hash, prefix, now,
	); err != nil {
		return "", fmt.Errorf("insert admin_tokens: %w", err)
	}
	_ = raw // returned to caller only
	return raw, nil
}

// Rotate replaces the active admin token. The previous raw token no longer verifies.
func (s *Service) Rotate() (raw string, err error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_tokens`).Scan(&n); err != nil {
		return "", fmt.Errorf("count admin_tokens: %w", err)
	}
	if n == 0 {
		return "", ErrNoAdminToken
	}
	raw, hash, prefix, err := generateTokenMaterial()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Single active token: replace the row in place (keep lowest id for stability).
	if _, err := s.db.Exec(
		`UPDATE admin_tokens SET token_hash = ?, key_prefix = ?, rotated_at = ? WHERE id = (SELECT id FROM admin_tokens ORDER BY id LIMIT 1)`,
		hash, prefix, now,
	); err != nil {
		return "", fmt.Errorf("update admin_tokens: %w", err)
	}
	return raw, nil
}

// Verify checks the raw token against the stored SHA-256 hash of the active token.
func (s *Service) Verify(raw string) (bool, error) {
	if raw == "" || !rawTokenRe.MatchString(raw) {
		return false, nil
	}
	hash := hashToken(raw)
	var stored string
	err := s.db.QueryRow(
		`SELECT token_hash FROM admin_tokens ORDER BY id DESC LIMIT 1`,
	).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select token_hash: %w", err)
	}
	return stored == hash, nil
}

// CurrentPrefix returns the display prefix of the active token (never the raw token).
func (s *Service) CurrentPrefix() (string, error) {
	var prefix string
	err := s.db.QueryRow(
		`SELECT key_prefix FROM admin_tokens ORDER BY id DESC LIMIT 1`,
	).Scan(&prefix)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoAdminToken
	}
	if err != nil {
		return "", fmt.Errorf("select key_prefix: %w", err)
	}
	return prefix, nil
}

func generateTokenMaterial() (raw, hashHex, displayPrefix string, err error) {
	suffix, err := idgen.RandomBase62(tokenRandomLen)
	if err != nil {
		return "", "", "", err
	}
	raw = tokenPrefixLabel + suffix
	hashHex = hashToken(raw)
	displayPrefix = raw
	if len(displayPrefix) > 8 {
		displayPrefix = displayPrefix[:8]
	}
	return raw, hashHex, displayPrefix, nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}