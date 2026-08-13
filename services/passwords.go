// Package services holds domain logic and reusable security utilities. It is
// testable without HTTP and is imported by handlers and the main package.
package services

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP recommended baseline).
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrPasswordTooShort is returned when a password is below the minimum length.
var ErrPasswordTooShort = errors.New("password must be at least 10 characters")

const minPasswordLen = 10

// ValidatePassword enforces policy for new passwords.
func ValidatePassword(password string) error {
	if len(password) < minPasswordLen {
		return ErrPasswordTooShort
	}
	if len(password) > 128 {
		return errors.New("password must be at most 128 characters")
	}
	return nil
}

// HashPassword derives an argon2id hash in the PHC string format:
// $argon2id$v=19$m=65536,t=1,p=4$<base64 salt>$<base64 key>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		enc.EncodeToString(salt), enc.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against a PHC-encoded argon2id hash.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("invalid password hash format")
	}
	var version int
	var memory uint32
	var timeIters uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("parse hash version: %w", err)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeIters, &threads); err != nil {
		return false, fmt.Errorf("parse hash parameters: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version %d", version)
	}
	dec := base64.RawStdEncoding
	salt, err := dec.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("decode salt: %w", err)
	}
	expected, err := dec.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("decode hash: %w", err)
	}
	actual := argon2.IDKey([]byte(password), salt, timeIters, memory, threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
