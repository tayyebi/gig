package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Dispute statuses.
const (
	DisputeOpen     = "open"
	DisputeResolved = "resolved"
)

// Dispute is a buyer or seller's escalation of an order for administrator
// review. There is no automated adjudication: resolution is always a
// documented decision recorded by an admin.
type Dispute struct {
	ID         int64
	OrderID    int64
	OpenedBy   int64
	Reason     string
	Status     string
	Decision   string
	ResolvedBy *int64
	ResolvedAt *time.Time
	CreatedAt  time.Time
	// InternalNotes is admin-only free text, never shown to the buyer or
	// seller, distinct from Decision which is the buyer/seller-visible
	// resolution rationale.
	InternalNotes string
}

// CreateDispute opens a dispute on an order.
func (s *Store) CreateDispute(ctx context.Context, orderID, openedBy int64, reason string) (*Dispute, error) {
	d := Dispute{OrderID: orderID, OpenedBy: openedBy, Reason: reason, Status: DisputeOpen}
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO disputes (order_id, opened_by, reason) VALUES ($1, $2, $3)
		RETURNING id, created_at`,
		orderID, openedBy, reason,
	).Scan(&d.ID, &d.CreatedAt); err != nil {
		return nil, fmt.Errorf("create dispute: %w", err)
	}
	return &d, nil
}

// GetOpenDispute returns an order's open dispute, if any.
func (s *Store) GetOpenDispute(ctx context.Context, orderID int64) (*Dispute, error) {
	return s.scanDispute(s.db.QueryRowContext(ctx, `
		SELECT id, order_id, opened_by, reason, status, decision, resolved_by, resolved_at, created_at
		FROM disputes WHERE order_id = $1 AND status = $2
		ORDER BY created_at DESC LIMIT 1`, orderID, DisputeOpen))
}

// GetLatestDispute returns the most recent dispute on an order, open or
// resolved, for display on the order timeline.
func (s *Store) GetLatestDispute(ctx context.Context, orderID int64) (*Dispute, error) {
	return s.scanDispute(s.db.QueryRowContext(ctx, `
		SELECT id, order_id, opened_by, reason, status, decision, resolved_by, resolved_at, created_at
		FROM disputes WHERE order_id = $1
		ORDER BY created_at DESC LIMIT 1`, orderID))
}

func (s *Store) scanDispute(row rowScanner) (*Dispute, error) {
	var d Dispute
	var resolvedBy sql.NullInt64
	var resolvedAt sql.NullTime
	err := row.Scan(&d.ID, &d.OrderID, &d.OpenedBy, &d.Reason, &d.Status, &d.Decision, &resolvedBy, &resolvedAt, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get dispute: %w", err)
	}
	if resolvedBy.Valid {
		v := resolvedBy.Int64
		d.ResolvedBy = &v
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		d.ResolvedAt = &t
	}
	return &d, nil
}

// ResolveDispute records an admin's decision on an open dispute.
func (s *Store) ResolveDispute(ctx context.Context, id, resolvedBy int64, decision string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE disputes SET status = $2, decision = $3, resolved_by = $4, resolved_at = now()
		WHERE id = $1 AND status = $5`,
		id, DisputeResolved, decision, resolvedBy, DisputeOpen,
	)
	if err != nil {
		return fmt.Errorf("resolve dispute %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
