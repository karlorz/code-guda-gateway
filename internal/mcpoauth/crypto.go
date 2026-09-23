package mcpoauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptN       = 16384
	scryptR       = 8
	scryptP       = 1
	scryptKeyLen  = 32
	saltByteCount = 16
)

// HashPassword generates a password hash string in format:
// scrypt$<N>$<r>$<p>$<salt-base64url>$urlsafe$<hash-base64url>
// Salt is random bytes, base64url encoded. The salt used for scrypt is the base64url string's UTF-8 bytes.
func HashPassword(password string) (string, error) {
	saltBytes := make([]byte, saltByteCount)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", fmt.Errorf("read random salt: %w", err)
	}
	saltStr := base64.RawURLEncoding.EncodeToString(saltBytes)
	hash, err := scrypt.Key([]byte(password), []byte(saltStr), scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return "", fmt.Errorf("scrypt.Key: %w", err)
	}
	hashB64 := base64.RawURLEncoding.EncodeToString(hash)
	return fmt.Sprintf("scrypt$%d$%d$%d$%s$urlsafe$%s", scryptN, scryptR, scryptP, saltStr, hashB64), nil
}

type scryptParams struct {
	n, r, p  int
	salt     string
	expected []byte
}

func parsePasswordHash(hashStr string) (scryptParams, error) {
	parts := strings.Split(hashStr, "$")
	if len(parts) != 7 {
		return scryptParams{}, errors.New("invalid hash format: parts count")
	}
	if parts[0] != "scrypt" {
		return scryptParams{}, errors.New("invalid hash algorithm")
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n <= 0 {
		return scryptParams{}, errors.New("invalid N")
	}
	r, err := strconv.Atoi(parts[2])
	if err != nil || r <= 0 {
		return scryptParams{}, errors.New("invalid r")
	}
	p, err := strconv.Atoi(parts[3])
	if err != nil || p <= 0 {
		return scryptParams{}, errors.New("invalid p")
	}
	if parts[4] == "" {
		return scryptParams{}, errors.New("empty salt")
	}
	if parts[5] != "urlsafe" {
		return scryptParams{}, errors.New("invalid encoding marker")
	}
	expectedHash, err := base64.RawURLEncoding.DecodeString(parts[6])
	if err != nil || len(expectedHash) != scryptKeyLen {
		return scryptParams{}, errors.New("invalid hash base64")
	}
	return scryptParams{n: n, r: r, p: p, salt: parts[4], expected: expectedHash}, nil
}

// VerifyPassword checks password against hashStr formatted as:
// scrypt$<N>$<r>$<p>$<salt-base64url>$urlsafe$<hash-base64url>
// Fails closed on any parse error or mismatch with constant-time comparison.
func VerifyPassword(password, hashStr string) bool {
	params, err := parsePasswordHash(hashStr)
	if err != nil {
		return false
	}
	derivedHash, err := scrypt.Key([]byte(password), []byte(params.salt), params.n, params.r, params.p, len(params.expected))
	if err != nil {
		return false
	}
	expectedHash := params.expected

	return subtle.ConstantTimeCompare(derivedHash, expectedHash) == 1
}

// ValidatePasswordHash checks if a hash string is structurally valid without comparing a password.
func ValidatePasswordHash(hashStr string) error {
	_, err := parsePasswordHash(hashStr)
	return err
}
