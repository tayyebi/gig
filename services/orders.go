package services

// Order lifecycle states (PLAN.md section 8). payout_pending and paid_out
// are declared for a complete vocabulary but are not reachable yet: real
// payouts require the provider work in later phases.
const (
	OrderPendingPayment    = "pending_payment"
	OrderPaymentFailed     = "payment_failed"
	OrderPaid              = "paid"
	OrderInProgress        = "in_progress"
	OrderDelivered         = "delivered"
	OrderRevisionRequested = "revision_requested"
	OrderAccepted          = "accepted"
	OrderDisputed          = "disputed"
	OrderCancelRequested   = "cancel_requested"
	OrderCancelled         = "cancelled"
	OrderPayoutPending     = "payout_pending"
	OrderPaidOut           = "paid_out"
	OrderClosed            = "closed"
)

// orderTransitions is the single source of truth for which order status
// changes are allowed. Handlers and the store must never infer a transition
// from client-supplied state; they call CanTransitionOrder (or the store's
// TransitionOrder, which re-checks the same rule against the current row).
var orderTransitions = map[string][]string{
	OrderPendingPayment:    {OrderPaid, OrderPaymentFailed},
	OrderPaymentFailed:     {OrderPendingPayment},
	OrderPaid:              {OrderInProgress},
	OrderInProgress:        {OrderDelivered, OrderCancelRequested, OrderDisputed},
	OrderDelivered:         {OrderRevisionRequested, OrderAccepted, OrderDisputed},
	OrderRevisionRequested: {OrderDelivered, OrderDisputed},
	OrderAccepted:          {OrderClosed, OrderDisputed},
	OrderCancelRequested:   {OrderCancelled, OrderInProgress},
	OrderDisputed:          {OrderInProgress, OrderClosed, OrderCancelled},
	OrderCancelled:         {},
	OrderClosed:            {},
	OrderPayoutPending:     {OrderPaidOut},
	OrderPaidOut:           {},
}

// CanTransitionOrder reports whether an order may move from `from` to `to`.
func CanTransitionOrder(from, to string) bool {
	for _, allowed := range orderTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// OrderTotals is the authoritative price breakdown for a checkout, computed
// entirely server-side from stored package/add-on prices. Money is always
// integer minor units; there is no floating point anywhere in this path.
type OrderTotals struct {
	SubtotalMinorUnits    int64
	PlatformFeeMinorUnits int64
	TotalMinorUnits       int64
}

// CalculateOrderTotals sums package and add-on prices and applies the
// platform fee in basis points (1000 = 10%), rounding the fee down to the
// nearest minor unit.
func CalculateOrderTotals(packagePriceMinorUnits int64, addonPricesMinorUnits []int64, platformFeeBps int) OrderTotals {
	subtotal := packagePriceMinorUnits
	for _, p := range addonPricesMinorUnits {
		subtotal += p
	}
	fee := subtotal * int64(platformFeeBps) / 10000
	return OrderTotals{
		SubtotalMinorUnits:    subtotal,
		PlatformFeeMinorUnits: fee,
		TotalMinorUnits:       subtotal + fee,
	}
}
