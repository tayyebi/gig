package services

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/tayyebi/gig/config"
)

// GenerateSessionToken creates a high-entropy raw session token and its
// SHA-256 hash. Only the hash is persisted; the raw token lives in the cookie.
func GenerateSessionToken() (raw, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(raw))
	return raw, fmt.Sprintf("%x", sum[:]), nil
}

// SessionCookie builds the session cookie with secure defaults.
func SessionCookie(cfg *config.Config, value string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     cfg.SessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

// ClearSessionCookie returns an expiring cookie that deletes the session.
func ClearSessionCookie(cfg *config.Config) *http.Cookie {
	return &http.Cookie{
		Name:     cfg.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}
