package adminauth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"code-guda-gateway/internal/idgen"
)

const (
	tokenPrefixLabel = "gat_"
	tokenRandomLen   = 32
	// Coolify SERVICE_PASSWORD_* values are typically 16–64 chars; allow headroom.
	minAdminSecretLen = 16
	maxAdminSecretLen = 128
)

var (
	// Legacy / CLI-generated admin tokens.
	rawTokenRe = regexp.MustCompile(`^gat_[A-Za-z0-9]{32}$`)

	ErrNoAdminToken    = errors.New("adminauth: no admin token configured")
	ErrInvalidToken    = errors.New("adminauth: invalid admin token")
	ErrTokenAlreadySet = errors.New("adminauth: admin token already initialized")
	// ErrSessionInvalid means the session id is missing, unknown, or expired (not a DB failure).
	ErrSessionInvalid = errors.New("adminauth: session invalid")
)

// Service provides admin token and session operations against SQLite.
type Service struct {
	db           *sql.DB
	sessionTTL   time.Duration
	cookieSecure bool
}

// Options configures browser session behavior.
type Options struct {
	CookieSecure bool
}

// NewService creates an auth service. sessionTTL is how long browser sessions last (e.g. 24h).
func NewService(db *sql.DB, sessionTTL time.Duration) *Service {
	return NewServiceWithOptions(db, sessionTTL, Options{CookieSecure: true})
}

// NewServiceWithOptions creates an auth service with explicit session cookie options.
func NewServiceWithOptions(db *sql.DB, sessionTTL time.Duration, opts Options) *Service {
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}
	return &Service{db: db, sessionTTL: sessionTTL, cookieSecure: opts.CookieSecure}
}

// Init creates the sole admin token row. The raw token is returned once and never stored.
func (s *Service) Init() (raw string, err error) {
	raw, hash, prefix, err := generateTokenMaterial()
	if err != nil {
		return "", err
	}
	if err := s.insertAdminToken(hash, prefix); err != nil {
		return "", err
	}
	return raw, nil
}

// InitFromRaw creates the sole admin token from an operator- or Coolify-supplied secret.
// Accepts classic gat_… tokens or Coolify-style SERVICE_PASSWORD values (see ValidAdminSecret).
// The raw secret is never stored; only its SHA-256 hash is persisted.
func (s *Service) InitFromRaw(raw string) error {
	raw = strings.TrimSpace(raw)
	if !ValidAdminSecret(raw) {
		return ErrInvalidToken
	}
	hash := hashToken(raw)
	prefix := displayPrefix(raw)
	return s.insertAdminToken(hash, prefix)
}

func (s *Service) insertAdminToken(hash, prefix string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_tokens`).Scan(&n); err != nil {
		return fmt.Errorf("count admin_tokens: %w", err)
	}
	if n > 0 {
		return ErrTokenAlreadySet
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO admin_tokens (token_hash, key_prefix, created_at) VALUES (?, ?, ?)`,
		hash, prefix, now,
	); err != nil {
		return fmt.Errorf("insert admin_tokens: %w", err)
	}
	return nil
}

// Rotate replaces the active admin token. The previous raw token no longer verifies.
func (s *Service) Rotate() (raw string, err error) {
	raw, hash, prefix, err := generateTokenMaterial()
	if err != nil {
		return "", err
	}
	if err := s.replaceAdminToken(hash, prefix); err != nil {
		return "", err
	}
	return raw, nil
}

// SetFromRaw sets the admin token hash to raw: init if empty, otherwise replace (rotate-to-value).
// Used when Coolify injects SERVICE_PASSWORD_* and the DB must match that env value.
// If the stored hash already matches raw, this is a no-op (sessions are preserved).
func (s *Service) SetFromRaw(raw string) error {
	raw = strings.TrimSpace(raw)
	if !ValidAdminSecret(raw) {
		return ErrInvalidToken
	}
	hash := hashToken(raw)
	prefix := displayPrefix(raw)
	has, err := s.HasToken()
	if err != nil {
		return err
	}
	if !has {
		return s.insertAdminToken(hash, prefix)
	}
	ok, err := s.Verify(raw)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return s.replaceAdminToken(hash, prefix)
}

func (s *Service) replaceAdminToken(hash, prefix string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_tokens`).Scan(&n); err != nil {
		return fmt.Errorf("count admin_tokens: %w", err)
	}
	if n == 0 {
		return ErrNoAdminToken
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// Single active token: replace the row in place (keep lowest id for stability).
	if _, err := s.db.Exec(
		`UPDATE admin_tokens SET token_hash = ?, key_prefix = ?, rotated_at = ? WHERE id = (SELECT id FROM admin_tokens ORDER BY id LIMIT 1)`,
		hash, prefix, now,
	); err != nil {
		return fmt.Errorf("update admin_tokens: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM admin_sessions`); err != nil {
		return fmt.Errorf("clear admin_sessions: %w", err)
	}
	return nil
}

// Verify checks the raw token against the stored SHA-256 hash of the active token.
func (s *Service) Verify(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !ValidAdminSecret(raw) {
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

// ValidAdminSecret reports whether raw may be used as an admin login secret.
// Accepts generated gat_… tokens and Coolify SERVICE_PASSWORD_* style secrets
// (16–128 printable non-space characters).
func ValidAdminSecret(raw string) bool {
	if rawTokenRe.MatchString(raw) {
		return true
	}
	if len(raw) < minAdminSecretLen || len(raw) > maxAdminSecretLen {
		return false
	}
	for _, r := range raw {
		if r > unicode.MaxASCII || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// HasToken reports whether an admin token row exists.
func (s *Service) HasToken() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_tokens`).Scan(&n); err != nil {
		return false, fmt.Errorf("count admin_tokens: %w", err)
	}
	return n > 0, nil
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

func generateTokenMaterial() (raw, hashHex, prefix string, err error) {
	suffix, err := idgen.RandomBase62(tokenRandomLen)
	if err != nil {
		return "", "", "", err
	}
	raw = tokenPrefixLabel + suffix
	hashHex = hashToken(raw)
	return raw, hashHex, displayPrefix(raw), nil
}

func displayPrefix(raw string) string {
	if len(raw) > 8 {
		return raw[:8]
	}
	return raw
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
