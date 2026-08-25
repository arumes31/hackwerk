package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const tokenBytes = 32

// NewToken returns a 256-bit URL-safe opaque value.
func NewToken() (string, error) {
	value := make([]byte, tokenBytes)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("auth: generating token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// TokenHash returns the fixed-size at-rest representation of an opaque token.
func TokenHash(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}
