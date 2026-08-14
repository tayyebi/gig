// Package ledger holds pure double-entry accounting logic: building balanced
// postings for a business event and validating that a set of entries
// balances. It performs no I/O; store/ledger.go persists what this package
// produces.
package ledger

import "fmt"

// Account kinds. Each (kind, owner, currency) tuple is a distinct ledger
// account (see migrations/0006_payments.sql).
const (
	AccountPlatformRevenue  = "platform_revenue"
	AccountSellerPending    = "seller_pending"
	AccountSellerAvailable  = "seller_available"
	AccountRefunds          = "refunds"
	AccountReserves         = "reserves"
	AccountProviderClearing = "provider_clearing"
)

const (
	DirectionDebit  = "debit"
	DirectionCredit = "credit"
)

// Entry is one unposted debit or credit, ready to be persisted by
// store.PostLedgerEntries inside the same transaction group.
type Entry struct {
	AccountKind string
	OwnerID     *int64 // nil for platform-owned accounts (revenue, clearing)
	Direction   string
	AmountMinor int64
	Currency    string
	OrderID     *int64
	Description string
}

// PaymentCaptured builds the balanced postings for a successful payment
// capture: gross funds move from the provider's clearing account into the
// platform's revenue (the fee share) and the seller's pending-earnings
// account (the payable share), per PLAN.md section 12. The provider clearing
// account is debited for the seller's payable so it does not compound its
// original credit into a double balance.
func PaymentCaptured(orderID, sellerID int64, grossMinor, feeMinor int64, currency string) ([]Entry, error) {
	if grossMinor <= 0 {
		return nil, fmt.Errorf("gross amount must be positive, got %d", grossMinor)
	}
	if feeMinor < 0 || feeMinor > grossMinor {
		return nil, fmt.Errorf("fee %d must be between 0 and gross %d", feeMinor, grossMinor)
	}
	payable := grossMinor - feeMinor
	entries := []Entry{
		{AccountKind: AccountProviderClearing, Direction: DirectionDebit, AmountMinor: grossMinor, Currency: currency, OrderID: &orderID, Description: "payment captured"},
	}
	if feeMinor > 0 {
		entries = append(entries, Entry{AccountKind: AccountPlatformRevenue, Direction: DirectionCredit, AmountMinor: feeMinor, Currency: currency, OrderID: &orderID, Description: "platform fee"})
	}
	if payable > 0 {
		entries = append(entries, Entry{AccountKind: AccountSellerPending, OwnerID: &sellerID, Direction: DirectionCredit, AmountMinor: payable, Currency: currency, OrderID: &orderID, Description: "seller payable, pending acceptance"})
	}
	return entries, Validate(entries)
}

// EarningsReleased moves a seller's payable from pending to available on
// order acceptance or auto-acceptance (PLAN.md section 8, step 11).
func EarningsReleased(orderID, sellerID int64, amountMinor int64, currency string) ([]Entry, error) {
	if amountMinor <= 0 {
		return nil, fmt.Errorf("amount must be positive, got %d", amountMinor)
	}
	entries := []Entry{
		{AccountKind: AccountSellerPending, OwnerID: &sellerID, Direction: DirectionDebit, AmountMinor: amountMinor, Currency: currency, OrderID: &orderID, Description: "seller earnings released"},
		{AccountKind: AccountSellerAvailable, OwnerID: &sellerID, Direction: DirectionCredit, AmountMinor: amountMinor, Currency: currency, OrderID: &orderID, Description: "seller earnings available"},
	}
	return entries, Validate(entries)
}

// RefundIssued reverses a captured payment: funds move out of the provider
// clearing account back to the buyer, drawn from platform revenue (fee
// share) and the seller's pending/available balance (payable share) in
// proportion to how the original capture was split, then routed through the
// refunds account for visibility. feeRefundMinor + payableRefundMinor must
// equal amountMinor.
func RefundIssued(orderID, sellerID int64, amountMinor, feeRefundMinor, payableRefundMinor int64, sellerFundsAvailable bool, currency string) ([]Entry, error) {
	if amountMinor <= 0 {
		return nil, fmt.Errorf("refund amount must be positive, got %d", amountMinor)
	}
	if feeRefundMinor+payableRefundMinor != amountMinor {
		return nil, fmt.Errorf("fee refund %d + payable refund %d must equal amount %d", feeRefundMinor, payableRefundMinor, amountMinor)
	}
	entries := []Entry{
		{AccountKind: AccountRefunds, Direction: DirectionDebit, AmountMinor: amountMinor, Currency: currency, OrderID: &orderID, Description: "refund issued"},
		{AccountKind: AccountProviderClearing, Direction: DirectionCredit, AmountMinor: amountMinor, Currency: currency, OrderID: &orderID, Description: "refund routed to buyer"},
	}
	if feeRefundMinor > 0 {
		entries = append(entries, Entry{AccountKind: AccountPlatformRevenue, Direction: DirectionDebit, AmountMinor: feeRefundMinor, Currency: currency, OrderID: &orderID, Description: "platform fee refunded"})
	}
	if payableRefundMinor > 0 {
		sellerAccount := AccountSellerPending
		if sellerFundsAvailable {
			sellerAccount = AccountSellerAvailable
		}
		entries = append(entries, Entry{AccountKind: sellerAccount, OwnerID: &sellerID, Direction: DirectionDebit, AmountMinor: payableRefundMinor, Currency: currency, OrderID: &orderID, Description: "seller payable refunded"})
	}
	return entries, Validate(entries)
}

// Validate reports an error unless entries is non-empty, every amount is
// positive, and total debits equal total credits within each currency —
// the invariant every posting function above must satisfy before its
// entries reach the store.
func Validate(entries []Entry) error {
	if len(entries) == 0 {
		return fmt.Errorf("ledger transaction must have at least one entry")
	}
	balances := map[string]int64{}
	for _, e := range entries {
		if e.AmountMinor <= 0 {
			return fmt.Errorf("ledger entry amount must be positive, got %d", e.AmountMinor)
		}
		switch e.Direction {
		case DirectionDebit:
			balances[e.Currency] -= e.AmountMinor
		case DirectionCredit:
			balances[e.Currency] += e.AmountMinor
		default:
			return fmt.Errorf("ledger entry direction must be %q or %q, got %q", DirectionDebit, DirectionCredit, e.Direction)
		}
	}
	for currency, balance := range balances {
		if balance != 0 {
			return fmt.Errorf("ledger transaction does not balance for %s: off by %d minor units", currency, balance)
		}
	}
	return nil
}
