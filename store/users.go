package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Roles a user may hold. Buyers and sellers coexist on the same account.
const (
	RoleBuyer  = "buyer"
	RoleSeller = "seller"
	RoleAdmin  = "admin"
)

// User statuses.
const (
	UserActive   = "active"
	UserDisabled = "disabled"
	UserDeleted  = "deleted"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("not found")

// ErrEmailTaken is returned when a normalized email already exists.
var ErrEmailTaken = errors.New("email already registered")

// ErrDisabled is returned when an account is not active.
var ErrDisabled = errors.New("account is disabled")

// User is an account identity. Only a password hash is ever stored.
type User struct {
	ID                  int64
	Email               string
	PasswordHash        string
	Name                string
	Locale              string
	Status              string
	EmailVerifiedAt     *time.Time
	TotpEnabled         bool
	FailedLoginAttempts int
	LockedUntil         *time.Time
	LastLoginAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// NormalizeEmail lowercases and trims an email address for unique lookups.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// CreateUser inserts a new user and grants the buyer role. email must be
// pre-validated; name must be non-empty.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, name, locale string) (int64, error) {
	if locale == "" {
		locale = "en"
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO users (email, email_lower, password_hash, name, locale)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		email, NormalizeEmail(email), passwordHash, name, locale,
	).Scan(&id)
	if isUniqueViolation(err) {
		return 0, ErrEmailTaken
	}
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}
	if err := s.AddRole(ctx, id, RoleBuyer); err != nil {
		return 0, err
	}
	return id, nil
}

// GetUserByEmail looks up a user by normalized email.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `
		SELECT id, email, password_hash, name, locale, status,
		       email_verified_at, totp_enabled_at, failed_login_attempts, locked_until,
		       last_login_at, created_at, updated_at
		FROM users
		WHERE email_lower = $1`,
		NormalizeEmail(email),
	))
}

// GetUserByID looks up a user by primary key.
func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `
		SELECT id, email, password_hash, name, locale, status,
		       email_verified_at, totp_enabled_at, failed_login_attempts, locked_until,
		       last_login_at, created_at, updated_at
		FROM users
		WHERE id = $1`,
		id,
	))
}

// SetEmailVerified marks the user's email as verified.
func (s *Store) SetEmailVerified(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET email_verified_at = coalesce(email_verified_at, now()), updated_at = now() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("verify email for user %d: %w", id, err)
	}
	return nil
}

// UpdateName changes the user's display name.
func (s *Store) UpdateName(ctx context.Context, id int64, name string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET name = $2, updated_at = now() WHERE id = $1`, id, name)
	if err != nil {
		return fmt.Errorf("update name for user %d: %w", id, err)
	}
	return nil
}

// UpdatePassword sets a new password hash.
func (s *Store) UpdatePassword(ctx context.Context, id int64, passwordHash string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, passwordHash)
	if err != nil {
		return fmt.Errorf("update password for user %d: %w", id, err)
	}
	return nil
}

// SetTOTP enables TOTP for a user, storing the secret.
func (s *Store) SetTOTP(ctx context.Context, id int64, secret string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret = $2, totp_enabled_at = now(), updated_at = now() WHERE id = $1`,
		id, secret)
	if err != nil {
		return fmt.Errorf("enable totp for user %d: %w", id, err)
	}
	return nil
}

// GetTOTPSecret returns the stored TOTP secret for verification. It is never
// exposed to templates.
func (s *Store) GetTOTPSecret(ctx context.Context, id int64) (string, error) {
	var secret string
	err := s.db.QueryRowContext(ctx, `SELECT coalesce(totp_secret, '') FROM users WHERE id = $1`, id).Scan(&secret)
	if err != nil {
		return "", fmt.Errorf("get totp secret for user %d: %w", id, err)
	}
	return secret, nil
}

// DisableTOTP disables TOTP and clears the stored secret.
func (s *Store) DisableTOTP(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, updated_at = now() WHERE id = $1`,
		id)
	if err != nil {
		return fmt.Errorf("disable totp for user %d: %w", id, err)
	}
	return nil
}

// SetLastLogin records the most recent successful sign-in and clears any
// login-failure state.
func (s *Store) SetLastLogin(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET last_login_at = now(), failed_login_attempts = 0, locked_until = NULL, updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("update last login for user %d: %w", id, err)
	}
	return nil
}

// RecordLoginFailure increments the failure counter and applies a lockout once
// the threshold is reached.
func (s *Store) RecordLoginFailure(ctx context.Context, id int64, maxAttempts int, lockout time.Duration) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users
		SET failed_login_attempts = failed_login_attempts + 1,
		    locked_until = CASE
		        WHEN failed_login_attempts + 1 >= $2 THEN now() + $3::interval
		        ELSE locked_until
		    END,
		    updated_at = now()
		WHERE id = $1`,
		id, maxAttempts, lockout.String())
	if err != nil {
		return fmt.Errorf("record login failure for user %d: %w", id, err)
	}
	return nil
}

// ResetLoginFailures clears a user's failure counter and lockout.
func (s *Store) ResetLoginFailures(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET failed_login_attempts = 0, locked_until = NULL WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("reset login failures for user %d: %w", id, err)
	}
	return nil
}

// LoginBlocked reports whether the account is locked at the given time.
func (u *User) LoginBlocked(now time.Time) bool {
	return u.LockedUntil != nil && u.LockedUntil.After(now)
}

// UserRoles returns the roles held by a user.
func (s *Store) UserRoles(ctx context.Context, id int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role FROM user_roles WHERE user_id = $1 ORDER BY role`, id)
	if err != nil {
		return nil, fmt.Errorf("user roles: %w", err)
	}
	defer rows.Close()

	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

// UserHasRole reports whether a user holds the given role.
func (s *Store) UserHasRole(ctx context.Context, id int64, role string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM user_roles WHERE user_id = $1 AND role = $2)`,
		id, role,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("user has role: %w", err)
	}
	return exists, nil
}

// AddRole grants a role (idempotent).
func (s *Store) AddRole(ctx context.Context, id int64, role string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_roles (user_id, role) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		id, role)
	if err != nil {
		return fmt.Errorf("add role %s to user %d: %w", role, id, err)
	}
	return nil
}

// RemoveRole revokes a role.
func (s *Store) RemoveRole(ctx context.Context, id int64, role string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM user_roles WHERE user_id = $1 AND role = $2`, id, role)
	if err != nil {
		return fmt.Errorf("remove role %s from user %d: %w", role, id, err)
	}
	return nil
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var totpEnabled sql.NullTime
	var verified sql.NullTime
	var lockedUntil sql.NullTime
	var lastLogin sql.NullTime
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.Locale, &u.Status,
		&verified, &totpEnabled, &u.FailedLoginAttempts, &lockedUntil, &lastLogin,
		&u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	if verified.Valid {
		t := verified.Time
		u.EmailVerifiedAt = &t
	}
	if lockedUntil.Valid {
		t := lockedUntil.Time
		u.LockedUntil = &t
	}
	if lastLogin.Valid {
		t := lastLogin.Time
		u.LastLoginAt = &t
	}
	u.TotpEnabled = totpEnabled.Valid
	return &u, nil
}
