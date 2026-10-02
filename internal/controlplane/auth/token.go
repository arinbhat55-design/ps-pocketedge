package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// RandomToken generates a 32-byte random token, hex-encoded. Used for both
// one-time enrollment tokens and long-lived agent bearer credentials —
// callers store only HashToken(token), never the plaintext.
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashToken returns the SHA-256 hex digest of token, for at-rest storage.
// Unlike passwords, these tokens are already high-entropy random values, so
// a fast hash (vs bcrypt) is appropriate — there's no brute-forceable
// keyspace to slow down.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
