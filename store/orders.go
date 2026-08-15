package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Order is a buyer's purchase of one gig package (plus optional add-ons).
// status is the single source of truth for the order state machine, enforced
// by services.CanTransitionOrder and re-checked atomically by TransitionOrder
// below; nothing about the allowed transitions is inferred from client input.
type Order struct {
	ID                    int64
	BuyerID               int64
	SellerID              int64
	GigID                 int64
	Status                string
	Requirements          string
	RevisionsUsed         int
	RevisionsAllowed      int
	SubtotalMinorUnits    int64
	PlatformFeeMinorUnits int64
	TotalMinorUnits       int64
	Currency              string
	PaidAt                *time.Time
	DeliveredAt           *time.Time
	AcceptedAt            *time.Time
	CancelledAt           *time.Time
	ClosedAt              *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// OrderItem is an immutable snapshot of one purchased package or add-on. It
// is never updated after insert, so later gig/package edits cannot rewrite
// financial history.
type OrderItem struct {
	ID              int64
	OrderID         int64
	Kind            string // "package" or "addon"
	Name            string
	Description     string
	PriceMinorUnits int64
	Currency        string
	DeliveryDays    *int
	CreatedAt       time.Time
}

// OrderItemInput is the pre-validated content of one order item snapshot,
// supplied by the caller (services.CalculateOrderTotals runs on the same
// prices before this is built) rather than computed in the store.
type OrderItemInput struct {
	Kind            string
	Name            string
	Description     string
	PriceMinorUnits int64
	Currency        string
	DeliveryDays    *int
}

// OrderSummary is the compact row shown in a buyer's or seller's order list.
type OrderSummary struct {
	ID               int64
	GigSlug          string
	GigTitle         string
	CounterpartyName string
	Status           string
	TotalMinorUnits  int64
	Currency         string
	CreatedAt        time.Time
}

// CreateOrderFromDraft inserts a new order in pending_payment status with its
// immutable item snapshot, and deletes the checkout draft it replaces. Totals
// and item prices must already be authoritatively computed by the caller from
// stored package/add-on prices, never from client input.
func (s *Store) CreateOrderFromDraft(ctx context.Context, draftID, buyerID, sellerID, gigID int64, requirements string, items []OrderItemInput, subtotal, fee, total int64, currency string, revisionsAllowed int) (*Order, error) {
	var o Order
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO orders (buyer_id, seller_id, gig_id, requirements, revisions_allowed,
			                     subtotal_minor_units, platform_fee_minor_units, total_minor_units, currency)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id, buyer_id, seller_id, gig_id, status, requirements, revisions_used, revisions_allowed,
			          subtotal_minor_units, platform_fee_minor_units, total_minor_units, currency, created_at, updated_at`,
			buyerID, sellerID, gigID, requirements, revisionsAllowed, subtotal, fee, total, currency,
		).Scan(&o.ID, &o.BuyerID, &o.SellerID, &o.GigID, &o.Status, &o.Requirements, &o.RevisionsUsed, &o.RevisionsAllowed,
			&o.SubtotalMinorUnits, &o.PlatformFeeMinorUnits, &o.TotalMinorUnits, &o.Currency, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return fmt.Errorf("create order: %w", err)
		}
		for _, it := range items {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO order_items (order_id, kind, name, description, price_minor_units, currency, delivery_days)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				o.ID, it.Kind, it.Name, it.Description, it.PriceMinorUnits, it.Currency, nullInt(it.DeliveryDays),
			); err != nil {
				return fmt.Errorf("create order item: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM draft_orders WHERE id = $1`, draftID); err != nil {
			return fmt.Errorf("delete converted draft order: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// GetOrder returns an order by ID. Callers are responsible for checking that
// the requester is the buyer, the seller, or an admin before showing it.
func (s *Store) GetOrder(ctx context.Context, id int64) (*Order, error) {
	o, err := scanOrder(s.db.QueryRowContext(ctx, orderSelectSQL+` WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order %d: %w", id, err)
	}
	return o, nil
}

// orderColumns is the fixed column order shared by every query that scans
// into an Order via scanOrder, so a SELECT and a RETURNING clause can never
// drift apart.
const orderColumns = `id, buyer_id, seller_id, gig_id, status, requirements, revisions_used, revisions_allowed,
	subtotal_minor_units, platform_fee_minor_units, total_minor_units, currency,
	paid_at, delivered_at, accepted_at, cancelled_at, closed_at, created_at, updated_at`

const orderSelectSQL = `SELECT ` + orderColumns + ` FROM orders`

func scanOrder(row rowScanner) (*Order, error) {
	var o Order
	if err := row.Scan(&o.ID, &o.BuyerID, &o.SellerID, &o.GigID, &o.Status, &o.Requirements, &o.RevisionsUsed, &o.RevisionsAllowed,
		&o.SubtotalMinorUnits, &o.PlatformFeeMinorUnits, &o.TotalMinorUnits, &o.Currency,
		&o.PaidAt, &o.DeliveredAt, &o.AcceptedAt, &o.CancelledAt, &o.ClosedAt, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

// GetOrderItems returns the immutable item snapshot for an order.
func (s *Store) GetOrderItems(ctx context.Context, orderID int64) ([]OrderItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, kind, name, description, price_minor_units, currency, delivery_days, created_at
		FROM order_items WHERE order_id = $1 ORDER BY id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list order items for order %d: %w", orderID, err)
	}
	defer rows.Close()

	var items []OrderItem
	for rows.Next() {
		var it OrderItem
		var delivery sql.NullInt64
		if err := rows.Scan(&it.ID, &it.OrderID, &it.Kind, &it.Name, &it.Description,
			&it.PriceMinorUnits, &it.Currency, &delivery, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		if delivery.Valid {
			v := int(delivery.Int64)
			it.DeliveryDays = &v
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

const orderSummarySQL = `
	SELECT o.id, g.slug, g.title, u.name, o.status, o.total_minor_units, o.currency, o.created_at,
	       count(*) OVER () AS total
	FROM orders o
	JOIN gigs g ON g.id = o.gig_id
	JOIN users u ON u.id = %s
	WHERE o.%s = $1
	ORDER BY o.created_at DESC
	LIMIT $2 OFFSET $3`

// ListOrdersForBuyer returns a page of orders a buyer has placed, most recent
// first, alongside the counterparty (seller) name.
func (s *Store) ListOrdersForBuyer(ctx context.Context, buyerID int64, page, perPage int) ([]OrderSummary, int, error) {
	return s.listOrders(ctx, fmt.Sprintf(orderSummarySQL, "o.seller_id", "buyer_id"), buyerID, page, perPage)
}

// ListOrdersForSeller returns a page of orders placed against a seller's
// gigs, most recent first, alongside the counterparty (buyer) name.
func (s *Store) ListOrdersForSeller(ctx context.Context, sellerID int64, page, perPage int) ([]OrderSummary, int, error) {
	return s.listOrders(ctx, fmt.Sprintf(orderSummarySQL, "o.buyer_id", "seller_id"), sellerID, page, perPage)
}

func (s *Store) listOrders(ctx context.Context, query string, id int64, page, perPage int) ([]OrderSummary, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	rows, err := s.db.QueryContext(ctx, query, id, perPage, (page-1)*perPage)
	if err != nil {
		return nil, 0, fmt.Errorf("list orders: %w", err)
	}
	defer rows.Close()

	var out []OrderSummary
	total := 0
	for rows.Next() {
		var r OrderSummary
		if err := rows.Scan(&r.ID, &r.GigSlug, &r.GigTitle, &r.CounterpartyName,
			&r.Status, &r.TotalMinorUnits, &r.Currency, &r.CreatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan order summary: %w", err)
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// TransitionOrder atomically moves an order from `from` to `to`, setting the
// matching milestone timestamp, and fails if the order's status has already
// changed (racing update, or the caller's view of `from` was stale). Callers
// must first check services.CanTransitionOrder(from, to); this only re-checks
// the current row still matches, it does not consult the transition table.
// CountRecentOrdersByBuyer returns how many orders a buyer has created in
// the given trailing window, for the fraud service's order-velocity rule
// (services/fraud.go).
func (s *Store) CountRecentOrdersByBuyer(ctx context.Context, buyerID int64, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM orders WHERE buyer_id = $1 AND created_at >= $2`, buyerID, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count recent orders for buyer %d: %w", buyerID, err)
	}
	return n, nil
}

func (s *Store) TransitionOrder(ctx context.Context, orderID int64, from, to string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE orders SET
			status       = $3,
			paid_at      = CASE WHEN $3 = 'paid'      THEN now() ELSE paid_at      END,
			delivered_at = CASE WHEN $3 = 'delivered' THEN now() ELSE delivered_at END,
			accepted_at  = CASE WHEN $3 = 'accepted'  THEN now() ELSE accepted_at  END,
			cancelled_at = CASE WHEN $3 = 'cancelled' THEN now() ELSE cancelled_at END,
			closed_at    = CASE WHEN $3 = 'closed'    THEN now() ELSE closed_at    END,
			updated_at   = now()
		WHERE id = $1 AND status = $2`,
		orderID, from, to,
	)
	if err != nil {
		return fmt.Errorf("transition order %d from %s to %s: %w", orderID, from, to, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AutoAcceptOrders transitions every order still in "delivered" status whose
// delivery is older than cutoff straight to "accepted", returning the orders
// that were moved so the caller can notify both parties.
func (s *Store) AutoAcceptOrders(ctx context.Context, cutoff time.Time) ([]Order, error) {
	rows, err := s.db.QueryContext(ctx, `
		UPDATE orders SET status = 'accepted', accepted_at = now(), updated_at = now()
		WHERE status = 'delivered' AND delivered_at < $1
		RETURNING `+orderColumns, cutoff)

	if err != nil {
		return nil, fmt.Errorf("auto-accept orders: %w", err)
	}
	defer rows.Close()

	var out []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan auto-accepted order: %w", err)
		}
		out = append(out, *o)
	}
	return out, rows.Err()
}

func nullInt(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}
