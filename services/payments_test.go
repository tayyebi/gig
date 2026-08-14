package services

import "testing"

func TestCanTransitionPayment(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{PaymentPending, PaymentSucceeded, true},
		{PaymentPending, PaymentFailed, true},
		{PaymentPending, PaymentProcessing, true},
		{PaymentProcessing, PaymentSucceeded, true},
		{PaymentSucceeded, PaymentFailed, false},
		{PaymentFailed, PaymentPending, false},
		{PaymentPending, PaymentPending, false},
	}
	for _, c := range cases {
		if got := CanTransitionPayment(c.from, c.to); got != c.want {
			t.Errorf("CanTransitionPayment(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestIdempotencyKeyIsDeterministic(t *testing.T) {
	a := IdempotencyKey("checkout", 42, 0)
	b := IdempotencyKey("checkout", 42, 0)
	if a != b {
		t.Errorf("IdempotencyKey must be deterministic for the same inputs: %q != %q", a, b)
	}
	c := IdempotencyKey("checkout", 42, 1)
	if a == c {
		t.Error("IdempotencyKey must differ across attempts")
	}
}
