package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

const gcmNonceSize = 12

// Encrypt seals plaintext with AES-256-GCM using key.
// The returned bytes are nonce || ciphertext || tag (GCM Seal output).
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encrypt: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("encrypt: nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens data produced by Encrypt using key.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("decrypt: key must be 32 bytes, got %d", len(key))
	}
	if len(ciphertext) < gcmNonceSize {
		return nil, errors.New("decrypt: ciphertext too short")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	nonce := ciphertext[:gcmNonceSize]
	sealed := ciphertext[gcmNonceSize:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}
