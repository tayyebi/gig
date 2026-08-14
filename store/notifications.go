package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Notification is an in-app record of a marketplace, order, or payment event
// relevant to one user.
type Notification struct {
	ID        int64
	UserID    int64
	Kind      string
	Body      string
	Link      string
	ReadAt    *time.Time
	CreatedAt time.Time
}

// CreateNotification records a notification for a user.
func (s *Store) CreateNotification(ctx context.Context, userID int64, kind, body, link string) (*Notification, error) {
	n := Notification{UserID: userID, Kind: kind, Body: body, Link: link}
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO notifications (user_id, kind, body, link) VALUES ($1, $2, $3, $4)
		RETURNING id, created_at`,
		userID, kind, body, link,
	).Scan(&n.ID, &n.CreatedAt); err != nil {
		return nil, fmt.Errorf("create notification: %w", err)
	}
	return &n, nil
}

// ListNotifications returns a page of a user's notifications, most recent
// first, alongside the total count across all pages.
func (s *Store) ListNotifications(ctx context.Context, userID int64, page, perPage int) ([]Notification, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, kind, body, link, read_at, created_at, count(*) OVER () AS total
		FROM notifications WHERE user_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, perPage, (page-1)*perPage,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications for user %d: %w", userID, err)
	}
	defer rows.Close()

	var out []Notification
	total := 0
	for rows.Next() {
		var n Notification
		var readAt sql.NullTime
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &n.Body, &n.Link, &readAt, &n.CreatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan notification: %w", err)
		}
		if readAt.Valid {
			t := readAt.Time
			n.ReadAt = &t
		}
		out = append(out, n)
	}
	return out, total, rows.Err()
}

// CountUnreadNotifications returns how many of a user's notifications are
// unread, for the navigation badge.
func (s *Store) CountUnreadNotifications(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, userID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count unread notifications for user %d: %w", userID, err)
	}
	return n, nil
}

// MarkAllNotificationsRead marks every unread notification for a user as
// read.
func (s *Store) MarkAllNotificationsRead(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`, userID,
	); err != nil {
		return fmt.Errorf("mark notifications read for user %d: %w", userID, err)
	}
	return nil
}
