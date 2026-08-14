package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrAlreadyReviewed is returned by CreateReview when the order already has
// a review (the schema enforces one review per order with a unique index).
var ErrAlreadyReviewed = errors.New("order already reviewed")

// Review is a buyer's rating and feedback on a completed order. It is
// unique per order (schema-enforced) so a buyer can review each purchase
// exactly once.
type Review struct {
	ID              int64
	OrderID         int64
	GigID           int64
	BuyerID         int64
	SellerID        int64
	Rating          int
	Body            string
	ModerationState string
	CreatedAt       time.Time
}

// CreateReview records a buyer's review of a completed order and recomputes
// the seller's public rating summary from every approved review, in the same
// transaction so the aggregate can never drift from the underlying rows.
func (s *Store) CreateReview(ctx context.Context, orderID, gigID, buyerID, sellerID int64, rating int, body string) (*Review, error) {
	rv := Review{OrderID: orderID, GigID: gigID, BuyerID: buyerID, SellerID: sellerID, Rating: rating, Body: body, ModerationState: ModerationApproved}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO reviews (order_id, gig_id, buyer_id, seller_id, rating, body)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
			orderID, gigID, buyerID, sellerID, rating, body,
		).Scan(&rv.ID, &rv.CreatedAt); err != nil {
			if isUniqueViolation(err) {
				return ErrAlreadyReviewed
			}
			return fmt.Errorf("create review: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE seller_profiles SET
				rating_avg = coalesce((SELECT avg(rating) FROM reviews WHERE seller_id = $1 AND moderation_state = 'approved'), 0),
				rating_count = (SELECT count(*) FROM reviews WHERE seller_id = $1 AND moderation_state = 'approved'),
				updated_at = now()
			WHERE user_id = $1`, sellerID,
		); err != nil {
			return fmt.Errorf("recalculate seller rating: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &rv, nil
}

// GetReviewByOrder returns the review left for an order, if any.
func (s *Store) GetReviewByOrder(ctx context.Context, orderID int64) (*Review, error) {
	var rv Review
	err := s.db.QueryRowContext(ctx, `
		SELECT id, order_id, gig_id, buyer_id, seller_id, rating, body, moderation_state, created_at
		FROM reviews WHERE order_id = $1`, orderID,
	).Scan(&rv.ID, &rv.OrderID, &rv.GigID, &rv.BuyerID, &rv.SellerID, &rv.Rating, &rv.Body, &rv.ModerationState, &rv.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get review for order %d: %w", orderID, err)
	}
	return &rv, nil
}
