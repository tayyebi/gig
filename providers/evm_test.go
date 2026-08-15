package providers

import "testing"

func TestConfirmationStatusReorgAndDepth(t *testing.T) {
	cases := []struct {
		name     string
		head     int64
		blockNum int64
		required int64
		want     string
	}{
		{"just mined, needs more confirmations", 100, 100, 6, StatusProcessing},
		{"exactly at threshold", 105, 100, 6, StatusSucceeded},
		{"well past threshold", 1000, 100, 6, StatusSucceeded},
		{"reorg dropped block back to head", 100, 100, 1, StatusSucceeded},
		{"zero confirmations required always succeeds", 50, 50, 0, StatusSucceeded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := confirmationStatus(c.head, c.blockNum, c.required)
			if got != c.want {
				t.Fatalf("confirmationStatus(%d, %d, %d) = %q, want %q", c.head, c.blockNum, c.required, got, c.want)
			}
		})
	}
}
