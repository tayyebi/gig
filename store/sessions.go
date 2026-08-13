package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Flash is a one-shot message shown on the next page load, stored in the
// session so PRG redirects can carry feedback without query parameters.
type Flash struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Session is a PostgreSQL-backed server session. Only the token hash and the
// CSRF token are stored; the raw session token lives solely in the cookie.
type Session struct {
	ID        int64
	UserID    *int64
	TokenHash string
	CSRF      string
	ExpiresAt time.Time
	UserAgent string
	IP        string
	RevokedAt *time.Time
	LastSeen  time.Time
	CreatedAt time.Time
}

// CreateSession inserts a new session and returns its primary key.
func (s *Store) CreateSession(ctx context.Context, userID *int64, tokenHash, csrf string, ttl time.Duration, userAgent, ip string) (int64, error) {
	if csrf == "" {
		return 0, fmt.Errorf("create session: csrf token is required")
	}
	var uid sql.NullInt64
	if userID != nil {
		uid = sql.NullInt64{Int64: *userID, Valid: true}
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO sessions (user_id, token_hash, csrf_token, expires_at, user_agent, ip)
		VALUES ($1, $2, $3, now() + $4::interval, $5, $6)
		RETURNING id`,
		uid, tokenHash, csrf, ttl.String(), userAgent, ip,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create session: %w", err)
	}
	return id, nil
}

// GetSessionByToken returns a live (not revoked, not expired) session.
func (s *Store) GetSessionByToken(ctx context.Context, tokenHash string) (*Session, error) {
	sess, err := scanSession(s.db.QueryRowContext(ctx, `
		SELECT id, user_id, token_hash, csrf_token, expires_at, user_agent, ip,
		       revoked_at, last_seen_at, created_at
		FROM sessions
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`,
		tokenHash,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sess, err
}

// GetSessionByID returns a session row regardless of validity (used for
// flash reads during the same request after login).
func (s *Store) GetSessionByID(ctx context.Context, id int64) (*Session, error) {
	sess, err := scanSession(s.db.QueryRowContext(ctx, `
		SELECT id, user_id, token_hash, csrf_token, expires_at, user_agent, ip,
		       revoked_at, last_seen_at, created_at
		FROM sessions
		WHERE id = $1`,
		id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sess, err
}

// TouchSession updates the last-seen timestamp (rate-limited by the caller).
func (s *Store) TouchSession(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("touch session %d: %w", id, err)
	}
	return nil
}

// AttachSessionUser promotes an anonymous session to an authenticated one
// during login, preserving flash and CSRF continuity.
func (s *Store) AttachSessionUser(ctx context.Context, sessionID, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET user_id = $2 WHERE id = $1`, sessionID, userID)
	if err != nil {
		return fmt.Errorf("attach user %d to session %d: %w", userID, sessionID, err)
	}
	return nil
}

// RotateSession revokes an existing session and creates a fresh one for the
// user, returning the new session id. It is used on privilege changes (login,
// password change, role change) so old tokens cannot be replayed.
func (s *Store) RotateSession(ctx context.Context, oldSessionID, userID int64, tokenHash, csrf string, ttl time.Duration, userAgent, ip string) (int64, error) {
	var newID int64
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, oldSessionID); err != nil {
			return fmt.Errorf("revoke old session: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO sessions (user_id, token_hash, csrf_token, expires_at, user_agent, ip)
			VALUES ($1, $2, $3, now() + $4::interval, $5, $6)
			RETURNING id`,
			userID, tokenHash, csrf, ttl.String(), userAgent, ip,
		).Scan(&newID); err != nil {
			return fmt.Errorf("create rotated session: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newID, nil
}

// RevokeSession revokes a single session.
func (s *Store) RevokeSession(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("revoke session %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeUserSessions revokes all of a user's sessions except the given one.
func (s *Store) RevokeUserSessions(ctx context.Context, userID, exceptID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND id <> $2 AND revoked_at IS NULL`,
		userID, exceptID)
	if err != nil {
		return fmt.Errorf("revoke sessions for user %d: %w", userID, err)
	}
	return nil
}

// SetFlash stores a one-shot message on a session.
func (s *Store) SetFlash(ctx context.Context, sessionID int64, flash *Flash) error {
	var raw any
	if flash != nil {
		b, err := json.Marshal(flash)
		if err != nil {
			return fmt.Errorf("marshal flash: %w", err)
		}
		raw = json.RawMessage(b)
	} else {
		raw = json.RawMessage("null")
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET flash = $2 WHERE id = $1`, sessionID, raw)
	if err != nil {
		return fmt.Errorf("set flash on session %d: %w", sessionID, err)
	}
	return nil
}

// ConsumeFlash reads and clears the flash on a session.
func (s *Store) ConsumeFlash(ctx context.Context, sessionID int64) (*Flash, error) {
	var raw []byte
	// The scalar subquery captures the pre-update value, which RETURNING alone
	// cannot (it only sees the new row).
	err := s.db.QueryRowContext(ctx, `
		WITH old_flash AS (
			SELECT flash FROM sessions WHERE id = $1
		)
		UPDATE sessions SET flash = 'null'::jsonb WHERE id = $1
		RETURNING (SELECT flash FROM old_flash)`,
		sessionID,
	).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("consume flash from session %d: %w", sessionID, err)
	}
	if string(raw) == "null" || len(raw) == 0 {
		return nil, nil
	}
	var f Flash
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("decode flash: %w", err)
	}
	return &f, nil
}

// DeleteExpiredSessions removes expired and long-stale sessions. Returns the
// number of rows deleted.
func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < now() OR revoked_at IS NOT NULL AND last_seen_at < now() - interval '7 days'`)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return res.RowsAffected()
}

func scanSession(row *sql.Row) (*Session, error) {
	var sess Session
	var uid sql.NullInt64
	var revoked sql.NullTime
	err := row.Scan(&sess.ID, &uid, &sess.TokenHash, &sess.CSRF, &sess.ExpiresAt,
		&sess.UserAgent, &sess.IP, &revoked, &sess.LastSeen, &sess.CreatedAt)
	if err != nil {
		return nil, err
	}
	if uid.Valid {
		id := uid.Int64
		sess.UserID = &id
	}
	if revoked.Valid {
		t := revoked.Time
		sess.RevokedAt = &t
	}
	return &sess, nil
}
