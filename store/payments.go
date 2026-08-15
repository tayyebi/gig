package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// PaymentIntent tracks one attempt to collect payment for an order via a
// provider (PLAN.md section 7, "Payments and Accounting").
type PaymentIntent struct {
	ID             int64
	OrderID        int64
	Provider       string
	ProviderRef    string
	ChargeRef      string
	Method         string
	Status         string
	AmountMinor    int64
	Currency       string
	IdempotencyKey string
	CheckoutURL    string
	ExpiresAt      *time.Time
	SucceededAt    *time.Time
	FailedAt       *time.Time
	CanceledAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

const paymentIntentColumns = `id, order_id, provider, provider_ref, charge_ref, method, status, amount_minor_units, currency,
	idempotency_key, checkout_url, expires_at, succeeded_at, failed_at, canceled_at, created_at, updated_at`

func scanPaymentIntent(row rowScanner) (*PaymentIntent, error) {
	var p PaymentIntent
	if err := row.Scan(&p.ID, &p.OrderID, &p.Provider, &p.ProviderRef, &p.ChargeRef, &p.Method, &p.Status, &p.AmountMinor, &p.Currency,
		&p.IdempotencyKey, &p.CheckoutURL, &p.ExpiresAt, &p.SucceededAt, &p.FailedAt, &p.CanceledAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreatePaymentIntent records a new payment attempt for an order. The
// idempotency key is unique, so retrying a checkout confirmation with the
// same key returns the original row instead of creating a duplicate charge
// attempt; callers should catch the unique-violation and re-fetch.
func (s *Store) CreatePaymentIntent(ctx context.Context, orderID int64, provider, method string, amountMinor int64, currency, idempotencyKey string) (*PaymentIntent, error) {
	p, err := scanPaymentIntent(s.db.QueryRowContext(ctx, `
		INSERT INTO payment_intents (order_id, provider, method, amount_minor_units, currency, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+paymentIntentColumns,
		orderID, provider, method, amountMinor, currency, idempotencyKey,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicateIdempotencyKey
		}
		return nil, fmt.Errorf("create payment intent for order %d: %w", orderID, err)
	}
	return p, nil
}

// ErrDuplicateIdempotencyKey is returned by CreatePaymentIntent when the
// idempotency key has already been used for a prior attempt.
var ErrDuplicateIdempotencyKey = errors.New("idempotency key already used")

// GetPaymentIntentByIdempotencyKey resolves an existing intent so a retried
// checkout confirmation can be resumed instead of double-charged.
func (s *Store) GetPaymentIntentByIdempotencyKey(ctx context.Context, key string) (*PaymentIntent, error) {
	p, err := scanPaymentIntent(s.db.QueryRowContext(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE idempotency_key = $1`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get payment intent by idempotency key: %w", err)
	}
	return p, nil
}

// GetPaymentIntent returns a payment intent by ID.
func (s *Store) GetPaymentIntent(ctx context.Context, id int64) (*PaymentIntent, error) {
	p, err := scanPaymentIntent(s.db.QueryRowContext(ctx, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get payment intent %d: %w", id, err)
	}
	return p, nil
}

// GetPaymentIntentByProviderRef resolves the intent a webhook event refers
// to by the provider's own reference (e.g. a Stripe Checkout Session ID).
func (s *Store) GetPaymentIntentByProviderRef(ctx context.Context, provider, providerRef string) (*PaymentIntent, error) {
	p, err := scanPaymentIntent(s.db.QueryRowContext(ctx,
		`SELECT `+paymentIntentColumns+` FROM payment_intents WHERE provider = $1 AND provider_ref = $2`, provider, providerRef))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get payment intent by provider ref: %w", err)
	}
	return p, nil
}

// LatestPaymentIntentForOrder returns the most recent payment attempt for an
// order, if any.
func (s *Store) LatestPaymentIntentForOrder(ctx context.Context, orderID int64) (*PaymentIntent, error) {
	p, err := scanPaymentIntent(s.db.QueryRowContext(ctx,
		`SELECT `+paymentIntentColumns+` FROM payment_intents WHERE order_id = $1 ORDER BY created_at DESC LIMIT 1`, orderID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest payment intent for order %d: %w", orderID, err)
	}
	return p, nil
}

// ListStalePaymentIntents returns pending/processing intents older than
// olderThan whose provider_ref is set, for the reconciliation sweep to
// re-check directly against the provider in case a webhook was dropped.
func (s *Store) ListStalePaymentIntents(ctx context.Context, olderThan time.Duration, limit int) ([]PaymentIntent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+paymentIntentColumns+`
		FROM payment_intents
		WHERE status IN ('pending', 'processing') AND provider_ref <> '' AND created_at < $1
		ORDER BY created_at LIMIT $2`, time.Now().Add(-olderThan), limit)
	if err != nil {
		return nil, fmt.Errorf("list stale payment intents: %w", err)
	}
	defer rows.Close()

	var out []PaymentIntent
	for rows.Next() {
		p, err := scanPaymentIntent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan stale payment intent: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// SetPaymentIntentSession attaches the provider's session/checkout reference
// once CreatePayment succeeds against the provider.
func (s *Store) SetPaymentIntentSession(ctx context.Context, id int64, providerRef, checkoutURL string, expiresAt *time.Time) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payment_intents SET provider_ref = $2, checkout_url = $3, expires_at = $4, updated_at = now()
		WHERE id = $1`, id, providerRef, checkoutURL, expiresAt)
	if err != nil {
		return fmt.Errorf("set payment intent session %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPaymentIntentChargeRef records the provider's refundable charge
// reference (e.g. Stripe's PaymentIntent ID), learned only once the
// checkout session completes — distinct from ProviderRef, which is the
// session ID used to create/look up the intent.
func (s *Store) SetPaymentIntentChargeRef(ctx context.Context, id int64, chargeRef string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE payment_intents SET charge_ref = $2, updated_at = now() WHERE id = $1`, id, chargeRef)
	if err != nil {
		return fmt.Errorf("set payment intent charge ref %d: %w", id, err)
	}
	return nil
}

// TransitionPaymentIntent atomically moves an intent from `from` to `to`,
// mirroring store.TransitionOrder: callers check services.CanTransitionPayment
// first, this only guards against a stale read of the current row.
func (s *Store) TransitionPaymentIntent(ctx context.Context, id int64, from, to string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payment_intents SET
			status       = $3,
			succeeded_at = CASE WHEN $3 = 'succeeded' THEN now() ELSE succeeded_at END,
			failed_at    = CASE WHEN $3 = 'failed'    THEN now() ELSE failed_at    END,
			canceled_at  = CASE WHEN $3 = 'canceled'  THEN now() ELSE canceled_at  END,
			updated_at   = now()
		WHERE id = $1 AND status = $2`, id, from, to)
	if err != nil {
		return fmt.Errorf("transition payment intent %d from %s to %s: %w", id, from, to, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordPaymentAttempt appends one raw provider status observation for an
// intent, kept for support and reconciliation even after the intent moves on.
func (s *Store) RecordPaymentAttempt(ctx context.Context, intentID int64, providerStatus, failureCode, failureMessage, payloadHash string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO payment_attempts (payment_intent_id, provider_status, failure_code, failure_message, raw_payload_hash)
		VALUES ($1, $2, $3, $4, $5)`, intentID, providerStatus, failureCode, failureMessage, payloadHash)
	if err != nil {
		return fmt.Errorf("record payment attempt for intent %d: %w", intentID, err)
	}
	return nil
}

// PaymentAttempt is one raw provider status observation recorded against a
// payment intent (e.g. a BTCPay invoice status change or an EVM
// confirmation-depth update from the reconciliation sweep), kept for
// support and admin visibility even after the intent's own status moves on.
type PaymentAttempt struct {
	ID              int64
	PaymentIntentID int64
	ProviderStatus  string
	FailureCode     string
	FailureMessage  string
	RawPayloadHash  string
	CreatedAt       time.Time
}

// ListPaymentAttemptsForIntent returns every raw status observation recorded
// for a payment intent, most recent first, for the admin per-order payment
// timeline (PLAN.md section 15, "complete order and payment timelines").
func (s *Store) ListPaymentAttemptsForIntent(ctx context.Context, intentID int64) ([]PaymentAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, payment_intent_id, provider_status, failure_code, failure_message, raw_payload_hash, created_at
		FROM payment_attempts
		WHERE payment_intent_id = $1
		ORDER BY created_at DESC`, intentID)
	if err != nil {
		return nil, fmt.Errorf("list payment attempts for intent %d: %w", intentID, err)
	}
	defer rows.Close()

	var out []PaymentAttempt
	for rows.Next() {
		var a PaymentAttempt
		if err := rows.Scan(&a.ID, &a.PaymentIntentID, &a.ProviderStatus, &a.FailureCode, &a.FailureMessage, &a.RawPayloadHash, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan payment attempt: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// InsertWebhookEvent records a verified provider webhook event, deduplicated
// by (provider, event_id). It returns inserted=false without error when the
// event has already been seen, so the caller can ack idempotently.
func (s *Store) InsertWebhookEvent(ctx context.Context, provider, eventID, eventType, providerRef string, payload []byte, payloadHash string) (id int64, inserted bool, err error) {
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO payment_webhook_events (provider, event_id, event_type, provider_ref, payload, payload_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (provider, event_id) DO NOTHING
		RETURNING id`, provider, eventID, eventType, providerRef, payload, payloadHash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("insert webhook event: %w", err)
	}
	return id, true, nil
}

// GetWebhookEventByProviderEventID resolves the persisted event a processing
// job's payload refers to.
func (s *Store) GetWebhookEventByProviderEventID(ctx context.Context, provider, eventID string) (*WebhookEvent, error) {
	var e WebhookEvent
	err := s.db.QueryRowContext(ctx, `
		SELECT id, provider, event_id, event_type, payload, status, attempts
		FROM payment_webhook_events WHERE provider = $1 AND event_id = $2`, provider, eventID,
	).Scan(&e.ID, &e.Provider, &e.EventID, &e.EventType, &e.Payload, &e.Status, &e.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get webhook event %s/%s: %w", provider, eventID, err)
	}
	return &e, nil
}

// PendingWebhookEvents returns events still awaiting processing, oldest
// first, for the reconciliation/retry job.
func (s *Store) PendingWebhookEvents(ctx context.Context, limit int) ([]WebhookEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, provider, event_id, event_type, payload, status, attempts
		FROM payment_webhook_events WHERE status = 'received' ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending webhook events: %w", err)
	}
	defer rows.Close()

	var out []WebhookEvent
	for rows.Next() {
		var e WebhookEvent
		if err := rows.Scan(&e.ID, &e.Provider, &e.EventID, &e.EventType, &e.Payload, &e.Status, &e.Attempts); err != nil {
			return nil, fmt.Errorf("scan webhook event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// WebhookEvent is a persisted provider webhook delivery.
type WebhookEvent struct {
	ID        int64
	Provider  string
	EventID   string
	EventType string
	Payload   []byte
	Status    string
	Attempts  int
}

// MarkWebhookProcessed records that an event was handled successfully.
func (s *Store) MarkWebhookProcessed(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE payment_webhook_events SET status = 'processed', processed_at = now(), attempts = attempts + 1 WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark webhook event %d processed: %w", id, err)
	}
	return nil
}

// MarkWebhookFailed records a processing failure so the reconciliation job
// can surface it; attempts is incremented for backoff/visibility.
func (s *Store) MarkWebhookFailed(ctx context.Context, id int64, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE payment_webhook_events SET status = 'failed', last_error = $2, attempts = attempts + 1 WHERE id = $1`, id, errMsg)
	if err != nil {
		return fmt.Errorf("mark webhook event %d failed: %w", id, err)
	}
	return nil
}

// CreateRefund records a refund request against a payment intent.
func (s *Store) CreateRefund(ctx context.Context, orderID, paymentIntentID int64, requestedBy *int64, reason string, amountMinor int64, currency string) (int64, error) {
	var uid sql.NullInt64
	if requestedBy != nil {
		uid = sql.NullInt64{Int64: *requestedBy, Valid: true}
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO refunds (order_id, payment_intent_id, requested_by, reason, amount_minor_units, currency)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		orderID, paymentIntentID, uid, reason, amountMinor, currency).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create refund for order %d: %w", orderID, err)
	}
	return id, nil
}

// SetRefundStatus updates a refund's terminal status and provider reference.
func (s *Store) SetRefundStatus(ctx context.Context, id int64, status, providerRef string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE refunds SET status = $2, provider_ref = $3, updated_at = now() WHERE id = $1`, id, status, providerRef)
	if err != nil {
		return fmt.Errorf("set refund %d status: %w", id, err)
	}
	return nil
}
