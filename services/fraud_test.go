package services

import "testing"

func TestIsVelocitySuspicious(t *testing.T) {
	if IsVelocitySuspicious(VelocityThreshold) {
		t.Errorf("count at threshold should not be suspicious")
	}
	if !IsVelocitySuspicious(VelocityThreshold + 1) {
		t.Errorf("count above threshold should be suspicious")
	}
}

func TestIsHighValue(t *testing.T) {
	if IsHighValue(HighValueThresholdMinorUnits - 1) {
		t.Errorf("amount below threshold should not be high value")
	}
	if !IsHighValue(HighValueThresholdMinorUnits) {
		t.Errorf("amount at threshold should be high value")
	}
}
