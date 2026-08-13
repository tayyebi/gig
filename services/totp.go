package services

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const totpPeriod = 30

// TOTPSecretLength is the number of random bytes in a generated TOTP secret.
const TOTPSecretLength = 20

// GenerateTOTPSecret returns a base32-encoded random secret for enrollment.
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, TOTPSecretLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// TOTPCode computes the RFC 6238 TOTP code for a secret at time t.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	counter := uint64(t.Unix()) / totpPeriod
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(msg)
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

// OTPAuthURI builds a standard otpauth:// setup URI for a label and issuer.
func OTPAuthURI(secret, account, issuer string) string {
	return fmt.Sprintf("otpauth://totp/%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		url.QueryEscape(account), secret, url.QueryEscape(issuer))
}

// VerifyTOTP checks a user-supplied code allowing skew steps before and after
func VerifyTOTP(secret, code string, skew int) (bool, error) {
	if skew < 0 {
		skew = 0
	}
	if skew > 5 {
		skew = 5
	}
	now := time.Now()
	expected, err := TOTPCode(secret, now)
	if err != nil {
		return false, err
	}
	if hmac.Equal([]byte(normalizeCode(code)), []byte(expected)) {
		return true, nil
	}
	for i := 1; i <= skew; i++ {
		before, err := TOTPCode(secret, now.Add(-time.Duration(i)*totpPeriod*time.Second))
		if err != nil {
			return false, err
		}
		if hmac.Equal([]byte(normalizeCode(code)), []byte(before)) {
			return true, nil
		}
	}
	return false, nil
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.TrimSpace(secret))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return nil, errors.New("totp secret is empty")
	}
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode totp secret: %w", err)
	}
	return key, nil
}

func normalizeCode(code string) string {
	return strings.TrimSpace(strings.ReplaceAll(code, " ", ""))
}
