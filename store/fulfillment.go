package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrRevisionLimitReached is returned when a buyer asks for another revision
// after using up the package's allowed count.
var ErrRevisionLimitReached = errors.New("revision limit reached")

// Delivery is one version of a seller's submitted work for an order.
type Delivery struct {
	ID        int64
	OrderID   int64
	SellerID  int64
	Version   int
	Message   string
	CreatedAt time.Time
}

// CreateDelivery records a new delivery version for an order. Versions are
// assigned under a row lock so two concurrent delivery submissions can never
// collide on the same version number.
func (s *Store) CreateDelivery(ctx context.Context, orderID, sellerID int64, message string) (*Delivery, error) {
	var d Delivery
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT id FROM orders WHERE id = $1 FOR UPDATE`, orderID); err != nil {
			return fmt.Errorf("lock order %d: %w", orderID, err)
		}
		var version int
		if err := tx.QueryRowContext(ctx,
			`SELECT coalesce(max(version), 0) + 1 FROM deliveries WHERE order_id = $1`, orderID,
		).Scan(&version); err != nil {
			return fmt.Errorf("compute delivery version: %w", err)
		}
		d = Delivery{OrderID: orderID, SellerID: sellerID, Version: version, Message: message}
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO deliveries (order_id, seller_id, version, message)
			VALUES ($1, $2, $3, $4) RETURNING id, created_at`,
			orderID, sellerID, version, message,
		).Scan(&d.ID, &d.CreatedAt); err != nil {
			return fmt.Errorf("create delivery: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ListDeliveries returns every delivery version submitted for an order,
// oldest first.
func (s *Store) ListDeliveries(ctx context.Context, orderID int64) ([]Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, seller_id, version, message, created_at
		FROM deliveries WHERE order_id = $1 ORDER BY version`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list deliveries for order %d: %w", orderID, err)
	}
	defer rows.Close()

	var out []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.OrderID, &d.SellerID, &d.Version, &d.Message, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevisionRequest is a buyer's request for changes to a delivered order.
type RevisionRequest struct {
	ID        int64
	OrderID   int64
	BuyerID   int64
	Reason    string
	CreatedAt time.Time
}

// CreateRevisionRequest records a revision request and consumes one unit of
// the order's revision allowance, atomically checking the limit under a row
// lock so concurrent requests cannot both slip in under the cap.
func (s *Store) CreateRevisionRequest(ctx context.Context, orderID, buyerID int64, reason string) (*RevisionRequest, error) {
	var rr RevisionRequest
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var used, allowed int
		if err := tx.QueryRowContext(ctx,
			`SELECT revisions_used, revisions_allowed FROM orders WHERE id = $1 FOR UPDATE`, orderID,
		).Scan(&used, &allowed); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock order %d: %w", orderID, err)
		}
		if used >= allowed {
			return ErrRevisionLimitReached
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE orders SET revisions_used = revisions_used + 1, updated_at = now() WHERE id = $1`, orderID,
		); err != nil {
			return fmt.Errorf("increment revisions used: %w", err)
		}
		rr = RevisionRequest{OrderID: orderID, BuyerID: buyerID, Reason: reason}
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO revision_requests (order_id, buyer_id, reason)
			VALUES ($1, $2, $3) RETURNING id, created_at`,
			orderID, buyerID, reason,
		).Scan(&rr.ID, &rr.CreatedAt); err != nil {
			return fmt.Errorf("create revision request: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &rr, nil
}

// ListRevisionRequests returns every revision request for an order, oldest
// first.
func (s *Store) ListRevisionRequests(ctx context.Context, orderID int64) ([]RevisionRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, buyer_id, reason, created_at
		FROM revision_requests WHERE order_id = $1 ORDER BY created_at`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list revision requests for order %d: %w", orderID, err)
	}
	defer rows.Close()

	var out []RevisionRequest
	for rows.Next() {
		var rr RevisionRequest
		if err := rows.Scan(&rr.ID, &rr.OrderID, &rr.BuyerID, &rr.Reason, &rr.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan revision request: %w", err)
		}
		out = append(out, rr)
	}
	return out, rows.Err()
}

// Cancellation request statuses.
const (
	CancellationPending  = "pending"
	CancellationApproved = "approved"
	CancellationDeclined = "declined"
)

// CancellationRequest is either party's request to cancel an in-progress
// order, subject to the counterparty's approval.
type CancellationRequest struct {
	ID          int64
	OrderID     int64
	RequestedBy int64
	Reason      string
	Status      string
	ResolvedAt  *time.Time
	CreatedAt   time.Time
}

// CreateCancellationRequest opens a cancellation request for an order.
func (s *Store) CreateCancellationRequest(ctx context.Context, orderID, requestedBy int64, reason string) (*CancellationRequest, error) {
	cr := CancellationRequest{OrderID: orderID, RequestedBy: requestedBy, Reason: reason, Status: CancellationPending}
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO cancellation_requests (order_id, requested_by, reason)
		VALUES ($1, $2, $3) RETURNING id, created_at`,
		orderID, requestedBy, reason,
	).Scan(&cr.ID, &cr.CreatedAt); err != nil {
		return nil, fmt.Errorf("create cancellation request: %w", err)
	}
	return &cr, nil
}

// GetOpenCancellationRequest returns an order's pending cancellation request,
// if any.
func (s *Store) GetOpenCancellationRequest(ctx context.Context, orderID int64) (*CancellationRequest, error) {
	var cr CancellationRequest
	var resolved sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, order_id, requested_by, reason, status, resolved_at, created_at
		FROM cancellation_requests WHERE order_id = $1 AND status = $2
		ORDER BY created_at DESC LIMIT 1`, orderID, CancellationPending,
	).Scan(&cr.ID, &cr.OrderID, &cr.RequestedBy, &cr.Reason, &cr.Status, &resolved, &cr.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get open cancellation request for order %d: %w", orderID, err)
	}
	if resolved.Valid {
		t := resolved.Time
		cr.ResolvedAt = &t
	}
	return &cr, nil
}

// ResolveCancellationRequest marks a pending cancellation request approved or
// declined.
func (s *Store) ResolveCancellationRequest(ctx context.Context, id int64, status string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE cancellation_requests SET status = $2, resolved_at = now()
		WHERE id = $1 AND status = $3`,
		id, status, CancellationPending,
	)
	if err != nil {
		return fmt.Errorf("resolve cancellation request %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
