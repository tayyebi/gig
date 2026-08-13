package services

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// CSRF token byte length. 32 random bytes -> 43-char URL-safe token.
const csrfTokenBytes = 32

// GenerateCSRF returns a new random CSRF token.
func GenerateCSRF() (string, error) {
	buf := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate csrf token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// VerifyCSRF compares a submitted token with the session's token in constant
// time.
func VerifyCSRF(submitted, stored string) bool {
	if submitted == "" || stored == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(submitted), []byte(stored)) == 1
}
