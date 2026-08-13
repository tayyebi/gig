package services

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashVerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=1,p=4$") {
		t.Fatalf("unexpected hash format: %q", hash)
	}
	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("correct password must verify")
	}
	ok, err = VerifyPassword("wrong password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("wrong password must not verify")
	}
}

func TestVerifyPasswordInvalidHash(t *testing.T) {
	if _, err := VerifyPassword("whatever", "not-a-hash"); err == nil {
		t.Error("malformed hash must return an error")
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err != ErrPasswordTooShort {
		t.Errorf("short password: got %v, want ErrPasswordTooShort", err)
	}
	if err := ValidatePassword("1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789"); err == nil {
		t.Error("over-long password must be rejected")
	}
	if err := ValidatePassword("long enough password"); err != nil {
		t.Errorf("valid password rejected: %v", err)
	}
}

func TestTOTPRoundTrip(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	code, err := TOTPCode(secret, time.Now())
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("code length = %d, want 6", len(code))
	}
	ok, err := VerifyTOTP(secret, code, 1)
	if err != nil {
		t.Fatalf("VerifyTOTP: %v", err)
	}
	if !ok {
		t.Error("current code must verify")
	}
	ok, err = VerifyTOTP(secret, "000000", 1)
	if err != nil {
		t.Fatalf("VerifyTOTP: %v", err)
	}
	if ok {
		t.Error("wrong code must not verify")
	}
}

func TestCSRF(t *testing.T) {
	tok, err := GenerateCSRF()
	if err != nil {
		t.Fatalf("GenerateCSRF: %v", err)
	}
	if !VerifyCSRF(tok, tok) {
		t.Error("matching token must verify")
	}
	if VerifyCSRF("attacker", tok) {
		t.Error("different token must not verify")
	}
	if VerifyCSRF("", tok) || VerifyCSRF(tok, "") {
		t.Error("empty token must not verify")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.Allow("k") {
			t.Fatalf("request %d within limit must pass", i+1)
		}
	}
	if rl.Allow("k") {
		t.Error("request past limit must be denied")
	}
	if !rl.Allow("other") {
		t.Error("unrelated key must be unaffected")
	}
}

func TestSessionToken(t *testing.T) {
	raw, hash, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}
	if len(raw) < 32 {
		t.Errorf("raw token too short: %q", raw)
	}
	if raw == hash {
		t.Error("raw and hash must differ")
	}
}
