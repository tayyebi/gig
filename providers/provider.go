// Package providers defines the normalized payment provider interface and
// its adapters. Provider-specific SDK types and API payloads stay inside
// each adapter file; the rest of the codebase only ever sees the normalized
// types defined here (PLAN.md section 9).
package providers

import (
	"context"
	"net/http"
	"time"
)

// CreatePaymentInput is everything an adapter needs to start collecting
// payment for one order.
type CreatePaymentInput struct {
	OrderID         int64
	AmountMinor     int64
	Currency        string // lowercase ISO 4217, e.g. "usd"
	Description     string
	IdempotencyKey  string
	SuccessURL      string
	CancelURL       string
	BuyerEmail      string
}

// PaymentSession is what the buyer is redirected to after a payment is
// created. Because the front end ships no JavaScript, every adapter must
// produce a URL the server can 303-redirect the buyer to (PLAN.md section 9,
// "Hosted Checkout Requirement").
type PaymentSession struct {
	ProviderRef string // provider's session/invoice/payment id
	CheckoutURL string
	ExpiresAt   time.Time
}

// Normalized payment statuses, shared across every adapter regardless of the
// provider's own vocabulary.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusCanceled   = "canceled"
	StatusExpired    = "expired"
)

// NormalizedPayment is a provider's payment state translated into the
// platform's own vocabulary.
type NormalizedPayment struct {
	ProviderRef   string
	ChargeRef     string // the provider's refundable reference (e.g. Stripe's PaymentIntent ID, distinct from the Checkout Session ID)
	Status        string
	AmountMinor   int64
	Currency      string
	FailureCode   string
	FailureReason string
}

// RefundInput requests a refund of a previously captured payment.
type RefundInput struct {
	ProviderRef    string // the provider's charge/payment-intent reference
	AmountMinor    int64
	Currency       string
	Reason         string
	IdempotencyKey string
}

// RefundResult is the provider's response to a refund request.
type RefundResult struct {
	ProviderRef string
	Status      string // one of the Status* constants
}

// ConnectAccountStatus is a seller's Stripe Connect account capability
// state, from either an account.updated webhook or a direct status check.
type ConnectAccountStatus struct {
	AccountID      string
	ChargesEnabled bool
	PayoutsEnabled bool
}

// VerifiedEvent is a provider webhook event that has passed signature
// verification, with its normalized event kind and the affected payment or
// (for Stripe Connect) seller account.
type VerifiedEvent struct {
	Provider    string
	EventID     string
	EventType   string
	ProviderRef string
	Payment     *NormalizedPayment
	Account     *ConnectAccountStatus
	RawPayload  []byte
}

// Provider is the adapter interface every payment rail (Stripe, BTCPay, an
// EVM network) implements. Business logic in services/ and handlers/ only
// ever depends on this interface, never on a provider SDK.
type Provider interface {
	Name() string
	CreatePayment(ctx context.Context, in CreatePaymentInput) (PaymentSession, error)
	Payment(ctx context.Context, providerRef string) (NormalizedPayment, error)
	Refund(ctx context.Context, in RefundInput) (RefundResult, error)
	VerifyWebhook(ctx context.Context, r *http.Request, body []byte) (VerifiedEvent, error)
	// ParseEvent decodes an already-verified, previously-persisted webhook
	// payload (no signature check) so the async processing job can re-derive
	// the normalized event without re-verifying against the original HTTP
	// request, which is gone by the time the job runs.
	ParseEvent(ctx context.Context, payload []byte) (VerifiedEvent, error)
}
