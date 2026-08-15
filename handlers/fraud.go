package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// fraudVelocityWindow is the trailing window checkFraudSignals queries when
// counting a buyer's recent orders for services.IsVelocitySuspicious.
const fraudVelocityWindow = time.Hour

// checkFraudSignals runs the velocity and high-value rules (services/fraud.go)
// against a freshly created order and records an audit entry — visible on
// the /admin/audit console added earlier in this phase — for any rule that
// fires. It never blocks the checkout; these are alerts for review, not a
// hold (an explicit admin hold/pause tool already exists for payouts and
// could be extended to orders as a follow-up if false positives prove
// costly enough to warrant it).
func (s *Server) checkFraudSignals(r *http.Request, buyerID int64, order *store.Order) {
	since := time.Now().Add(-fraudVelocityWindow)
	count, err := s.Store.CountRecentOrdersByBuyer(r.Context(), buyerID, since)
	if err != nil {
		s.Log.Warn("fraud velocity check failed", "error", err, "buyer_id", buyerID)
	} else if services.IsVelocitySuspicious(count) {
		s.audit(r.Context(), nil, r, "fraud.velocity_alert", "order", strconv.FormatInt(order.ID, 10),
			map[string]any{"buyer_id": buyerID, "recent_order_count": count, "window": fraudVelocityWindow.String()})
	}
	if services.IsHighValue(order.TotalMinorUnits) {
		s.audit(r.Context(), nil, r, "fraud.high_value_alert", "order", strconv.FormatInt(order.ID, 10),
			map[string]any{"buyer_id": buyerID, "total_minor_units": order.TotalMinorUnits, "currency": order.Currency})
	}
}

// alertWalletChange records an audited alert whenever a seller's payout
// wallet changes, independent of the cooldown gate already enforced by
// store.ConfirmWallet — the cooldown controls when the new address becomes
// eligible for payouts, this makes the change itself visible for review.
func (s *Server) alertWalletChange(r *http.Request, userID, walletID int64, network, asset string) {
	s.audit(r.Context(), nil, r, "fraud.wallet_change_alert", "seller_wallet", strconv.FormatInt(walletID, 10),
		map[string]any{"user_id": userID, "network": network, "asset": asset})
}
