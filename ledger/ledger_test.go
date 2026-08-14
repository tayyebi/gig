package ledger

import "testing"

func TestPaymentCapturedBalances(t *testing.T) {
	entries, err := PaymentCaptured(1, 2, 10000, 1000, "usd")
	if err != nil {
		t.Fatalf("PaymentCaptured: %v", err)
	}
	if err := Validate(entries); err != nil {
		t.Fatalf("entries do not balance: %v", err)
	}
	var clearing, revenue, pending int64
	for _, e := range entries {
		amt := e.AmountMinor
		if e.Direction == DirectionDebit {
			amt = -amt
		}
		switch e.AccountKind {
		case AccountProviderClearing:
			clearing += amt
		case AccountPlatformRevenue:
			revenue += amt
		case AccountSellerPending:
			pending += amt
		}
	}
	if clearing != -10000 {
		t.Errorf("clearing = %d, want -10000", clearing)
	}
	if revenue != 1000 {
		t.Errorf("revenue = %d, want 1000", revenue)
	}
	if pending != 9000 {
		t.Errorf("pending = %d, want 9000", pending)
	}
}

func TestPaymentCapturedRejectsInvalidFee(t *testing.T) {
	if _, err := PaymentCaptured(1, 2, 1000, 2000, "usd"); err == nil {
		t.Fatal("expected error for fee exceeding gross amount")
	}
	if _, err := PaymentCaptured(1, 2, 0, 0, "usd"); err == nil {
		t.Fatal("expected error for zero gross amount")
	}
}

func TestEarningsReleasedBalances(t *testing.T) {
	entries, err := EarningsReleased(1, 2, 9000, "usd")
	if err != nil {
		t.Fatalf("EarningsReleased: %v", err)
	}
	if err := Validate(entries); err != nil {
		t.Fatalf("entries do not balance: %v", err)
	}
}

func TestRefundIssuedBalances(t *testing.T) {
	entries, err := RefundIssued(1, 2, 10000, 1000, 9000, false, "usd")
	if err != nil {
		t.Fatalf("RefundIssued: %v", err)
	}
	if err := Validate(entries); err != nil {
		t.Fatalf("entries do not balance: %v", err)
	}
}

func TestRefundIssuedRejectsMismatchedSplit(t *testing.T) {
	if _, err := RefundIssued(1, 2, 10000, 1000, 8000, false, "usd"); err == nil {
		t.Fatal("expected error when fee+payable refund does not equal amount")
	}
}

func TestValidateRejectsUnbalancedEntries(t *testing.T) {
	entries := []Entry{
		{AccountKind: AccountProviderClearing, Direction: DirectionDebit, AmountMinor: 100, Currency: "usd"},
	}
	if err := Validate(entries); err == nil {
		t.Fatal("expected error for a single unbalanced entry")
	}
}

func TestValidateRejectsMixedCurrencyImbalance(t *testing.T) {
	entries := []Entry{
		{AccountKind: AccountProviderClearing, Direction: DirectionDebit, AmountMinor: 100, Currency: "usd"},
		{AccountKind: AccountPlatformRevenue, Direction: DirectionCredit, AmountMinor: 100, Currency: "eur"},
	}
	if err := Validate(entries); err == nil {
		t.Fatal("expected error: each currency must balance independently")
	}
}

func TestValidateRejectsNonPositiveAmount(t *testing.T) {
	entries := []Entry{
		{AccountKind: AccountProviderClearing, Direction: DirectionDebit, AmountMinor: 0, Currency: "usd"},
		{AccountKind: AccountPlatformRevenue, Direction: DirectionCredit, AmountMinor: 0, Currency: "usd"},
	}
	if err := Validate(entries); err == nil {
		t.Fatal("expected error for zero-amount entry")
	}
}
