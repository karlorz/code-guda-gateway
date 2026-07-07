package secrets

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const masterKeySize = 32

// LoadOrCreate loads a 32-byte master key from path, or creates it if missing.
// New files are written with mode 0600. Existing files must be exactly 32 bytes.
func LoadOrCreate(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read master key file: %w", err)
		}
		key := make([]byte, masterKeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate master key: %w", err)
		}
		dir := filepath.Dir(path)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("create master key directory: %w", err)
			}
		}
		if err := os.WriteFile(path, key, 0o600); err != nil {
			return nil, fmt.Errorf("write master key file: %w", err)
		}
		return key, nil
	}

	if len(data) != masterKeySize {
		return nil, fmt.Errorf("master key file %q: invalid size %d bytes, want %d", path, len(data), masterKeySize)
	}
	key := make([]byte, masterKeySize)
	copy(key, data)
	return key, nil
}
