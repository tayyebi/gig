package services

import "fmt"

// Payment intent lifecycle states, normalized across providers (PLAN.md
// section 9). Mirrors the shape of the order-status transition table in
// orders.go: a pure map plus a checker, re-verified atomically by
// store.TransitionPaymentIntent.
const (
	PaymentPending    = "pending"
	PaymentProcessing = "processing"
	PaymentSucceeded  = "succeeded"
	PaymentFailed     = "failed"
	PaymentCanceled   = "canceled"
	PaymentExpired    = "expired"
)

var paymentTransitions = map[string][]string{
	PaymentPending:    {PaymentProcessing, PaymentSucceeded, PaymentFailed, PaymentCanceled, PaymentExpired},
	PaymentProcessing: {PaymentSucceeded, PaymentFailed},
	PaymentSucceeded:  {},
	PaymentFailed:     {},
	PaymentCanceled:   {},
	PaymentExpired:    {},
}

// CanTransitionPayment reports whether a payment intent may move from
// `from` to `to`.
func CanTransitionPayment(from, to string) bool {
	if from == to {
		return false
	}
	for _, allowed := range paymentTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// IdempotencyKey deterministically derives the key for a money-changing
// request (checkout confirmation, refund) from its scope and a monotonic
// attempt counter. PLAN.md section 13 requires idempotency keys on every
// such request; deriving rather than randomizing means a double-submitted
// form (e.g. a buyer clicking "confirm" twice) reuses the same key and hits
// the store's unique-constraint dedup instead of creating a second charge.
func IdempotencyKey(scope string, entityID int64, attempt int) string {
	return fmt.Sprintf("%s:%d:%d", scope, entityID, attempt)
}
