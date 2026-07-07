package idgen

import (
	"crypto/rand"
	"fmt"
)

var base62Alphabet = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789")

// RandomBase62 returns a cryptographically random string of length n using base62.
func RandomBase62(n int) (string, error) {
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