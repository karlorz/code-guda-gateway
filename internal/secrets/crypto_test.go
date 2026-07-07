package secrets_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"code-guda-gateway/internal/secrets"
)

func randomKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return key
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Parallel()
	key := randomKey(t)
	plaintext := []byte("sk-provider-secret-key-value")

	ciphertext, err := secrets.Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := secrets.Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %q", got)
	}
}

func TestEncrypt_CiphertextIsNotPlaintext(t *testing.T) {
	t.Parallel()
	key := randomKey(t)
	plaintext := []byte("super-secret-provider-api-key")

	ciphertext, err := secrets.Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("ciphertext contains plaintext substring")
	}
	if bytes.Contains(ciphertext, key) {
		t.Fatal("ciphertext contains master key substring")
	}
}

func TestEncrypt_NonceIsRandom(t *testing.T) {
	t.Parallel()
	key := randomKey(t)
	plaintext := []byte("same-plaintext-every-time")

	ct1, err := secrets.Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}
	ct2, err := secrets.Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}
	if bytes.Equal(ct1, ct2) {
		t.Fatal("two encryptions produced identical ciphertext; nonce should differ")
	}
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	t.Parallel()
	keyA := randomKey(t)
	keyB := randomKey(t)
	if bytes.Equal(keyA, keyB) {
		t.Fatal("test setup: keys must differ")
	}
	plaintext := []byte("provider-key-material")

	ciphertext, err := secrets.Encrypt(keyA, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := secrets.Decrypt(keyB, ciphertext)
	if err == nil {
		t.Fatalf("Decrypt with wrong key succeeded; got %q", got)
	}
	if got != nil && len(got) > 0 {
		t.Fatalf("unexpected plaintext returned: %q", got)
	}
}

func TestDecrypt_TamperedCiphertextFails(t *testing.T) {
	t.Parallel()
	key := randomKey(t)
	plaintext := []byte("tamper-test")

	ciphertext, err := secrets.Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(ciphertext) < 13 {
		t.Fatalf("ciphertext too short: %d", len(ciphertext))
	}
	ciphertext[len(ciphertext)-1] ^= 0xff

	_, err = secrets.Decrypt(key, ciphertext)
	if err == nil {
		t.Fatal("Decrypt after tamper should fail")
	}
}

func TestDecrypt_ShortInputFails(t *testing.T) {
	t.Parallel()
	key := randomKey(t)

	cases := []struct {
		name  string
		input []byte
	}{
		{"empty", nil},
		{"empty slice", []byte{}},
		{"too short", []byte{1, 2, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := secrets.Decrypt(key, tc.input)
			if err == nil {
				t.Fatal("expected error for short input")
			}
		})
	}
}
