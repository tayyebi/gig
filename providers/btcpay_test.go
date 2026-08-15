package providers

import "testing"

// TestNormalizeInvoiceStatusPartialAndOverpayment covers PLAN.md section 9's
// explicit requirement to "handle underpayment, overpayment, expiration,
// partial payment, and refund workflows explicitly" (TODO.md Phase 8
// reliability: "test partial, under, and overpayments").
func TestNormalizeInvoiceStatusPartialAndOverpayment(t *testing.T) {
	cases := []struct {
		name         string
		status, addl string
		want         string
	}{
		{"new invoice is pending", "New", "", StatusPending},
		{"processing invoice", "Processing", "", StatusProcessing},
		{"fully settled invoice succeeds", "Settled", "", StatusSucceeded},
		{"settled but underpaid is not silently succeeded", "Settled", "PaidPartial", StatusProcessing},
		{"settled but paid late is not silently succeeded", "Settled", "PaidLate", StatusProcessing},
		{"overpayment on a settled invoice still succeeds", "Settled", "PaidOver", StatusSucceeded},
		{"expired invoice fails to expired", "Expired", "", StatusExpired},
		{"expired but partially paid still surfaces as expired for review", "Expired", "PaidPartial", StatusExpired},
		{"invalid invoice fails", "Invalid", "", StatusFailed},
		{"unknown status defaults to pending", "SomethingNew", "", StatusPending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeInvoiceStatus(c.status, c.addl)
			if got != c.want {
				t.Errorf("normalizeInvoiceStatus(%q, %q) = %q, want %q", c.status, c.addl, got, c.want)
			}
		})
	}
}
