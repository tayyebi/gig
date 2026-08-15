package services

// Package-level fraud/risk thresholds (PLAN.md section 17: "Add fraud
// controls for velocity, suspicious order patterns, chargebacks, wallet
// changes, and high-value transactions"). These are pure decision
// functions — no I/O, no store dependency — so they are trivial to unit
// test; the handler layer (handlers/checkout.go, handlers/wallets.go) is
// responsible for gathering the counts/amounts and recording an audit
// entry when a rule fires. Alerts are surfaced through the existing
// AuditLog/`/admin/audit` console (Phase 8) rather than a separate alerts
// table, so every fired rule is visible alongside every other privileged
// or notable action without a second review surface to keep in sync.

// VelocityThreshold is the number of orders a single buyer may place within
// VelocityWindow before the order-velocity rule fires. It is intentionally
// a package variable rather than a config-driven value for this pass —
// operators who need to tune it can do so via a future platform_settings
// entry (the settings console added in this phase already supports adding
// arbitrary keys, so wiring this in is a small follow-up).
var VelocityThreshold = 5

// HighValueThresholdMinorUnits flags any single order or payout at or above
// this amount (in the order's minor currency units) for admin review.
// $2,000.00 in a 2-decimal currency, as a reasonable initial default.
var HighValueThresholdMinorUnits int64 = 200000

// IsVelocitySuspicious reports whether recentOrderCount (orders placed by
// one buyer within the trailing window the caller queried) exceeds the
// configured threshold.
func IsVelocitySuspicious(recentOrderCount int) bool {
	return recentOrderCount > VelocityThreshold
}

// IsHighValue reports whether a transaction amount warrants a high-value
// alert.
func IsHighValue(amountMinorUnits int64) bool {
	return amountMinorUnits >= HighValueThresholdMinorUnits
}
