package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Auth token kinds.
const (
	TokenEmailVerification = "email_verification"
	TokenPasswordReset     = "password_reset"
)

// AuthToken is a one-time, expiring token for email verification or password
// reset. Only the token hash is stored.
type AuthToken struct {
	ID        int64
	UserID    int64
	Kind      string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// CreateAuthToken inserts a one-time token.
func (s *Store) CreateAuthToken(ctx context.Context, userID int64, kind, tokenHash string, ttl time.Duration) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO auth_tokens (user_id, kind, token_hash, expires_at)
		VALUES ($1, $2, $3, now() + $4::interval)
		RETURNING id`,
		userID, kind, tokenHash, ttl.String(),
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create auth token: %w", err)
	}
	return id, nil
}

// GetAuthToken returns an unused, unexpired token of the given kind.
func (s *Store) GetAuthToken(ctx context.Context, kind, tokenHash string) (*AuthToken, error) {
	var t AuthToken
	var used sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, kind, token_hash, expires_at, used_at, created_at
		FROM auth_tokens
		WHERE kind = $1 AND token_hash = $2 AND used_at IS NULL AND expires_at > now()`,
		kind, tokenHash,
	).Scan(&t.ID, &t.UserID, &t.Kind, &t.TokenHash, &t.ExpiresAt, &used, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get auth token: %w", err)
	}
	if used.Valid {
		tm := used.Time
		t.UsedAt = &tm
	}
	return &t, nil
}

// UseAuthToken marks a token consumed (idempotent).
func (s *Store) UseAuthToken(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE auth_tokens SET used_at = now() WHERE id = $1 AND used_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("use auth token %d: %w", id, err)
	}
	return nil
}

// DeleteExpiredAuthTokens removes used and expired tokens.
func (s *Store) DeleteExpiredAuthTokens(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM auth_tokens WHERE expires_at < now() OR used_at IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("delete expired auth tokens: %w", err)
	}
	return res.RowsAffected()
}
