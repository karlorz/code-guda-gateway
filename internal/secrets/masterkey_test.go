package secrets_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"code-guda-gateway/internal/secrets"
)

func TestLoadOrCreate_CreatesKeyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")

	key, err := secrets.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("key len = %d, want 32", len(key))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) != 32 {
		t.Fatalf("file len = %d, want 32", len(data))
	}
	if !bytes.Equal(key, data) {
		t.Fatal("returned key does not match file contents")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestLoadOrCreate_LoadsExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	want := bytes.Repeat([]byte{0xab}, 32)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	key, err := secrets.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if !bytes.Equal(key, want) {
		t.Fatal("returned key does not match existing file")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(after, want) {
		t.Fatal("file was modified")
	}
}

func TestLoadOrCreate_RejectsWrongSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	if err := os.WriteFile(path, make([]byte, 16), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := secrets.LoadOrCreate(path)
	if err == nil {
		t.Fatal("expected error for wrong-sized key file")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(after) != 16 {
		t.Fatalf("file len = %d, want 16 (unchanged)", len(after))
	}
}