package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tayyebi/gig/ledger"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// orderAutoAcceptSweepKind is a recurring job, the same self-rescheduling
// shape as maintenanceSweepKind: exactly one sweep is ever outstanding, even
// with multiple workers.
const orderAutoAcceptSweepKind = "order.autoaccept_sweep"

// registerOrderJobs wires the recurring auto-accept sweep and seeds the
// first run.
func registerOrderJobs(q *jobQueue, jc *jobContext) error {
	q.Register(orderAutoAcceptSweepKind, orderAutoAcceptSweep)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := q.st.EnqueueJob(ctx, orderAutoAcceptSweepKind, nil, time.Now(), q.cfg.JobMaxAttempts); err != nil {
		return fmt.Errorf("seed order auto-accept sweep: %w", err)
	}
	return nil
}

// orderAutoAcceptSweep moves every delivery whose review window
// (AUTO_ACCEPT_AFTER) has elapsed straight to "accepted", notifies the
// seller, then reschedules itself. It reuses MaintenanceInterval as its
// cadence rather than introduce a second polling-interval setting.
func orderAutoAcceptSweep(ctx context.Context, jc *jobContext, _ store.Job) error {
	cutoff := time.Now().Add(-jc.Cfg.AutoAcceptAfter)
	orders, err := jc.Store.AutoAcceptOrders(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("auto-accept orders: %w", err)
	}
	for _, o := range orders {
		releaseSellerEarnings(ctx, jc, &o)
		notifyOrderParty(ctx, jc, o.SellerID, "order.accepted",
			fmt.Sprintf("Order #%d was automatically accepted", o.ID), fmt.Sprintf("/orders/%d", o.ID))
	}
	jc.Log.Info("auto-accept sweep complete", "orders_accepted", len(orders))

	next := time.Now().Add(jc.Cfg.MaintenanceInterval)
	if _, err := jc.Store.EnqueueJob(ctx, orderAutoAcceptSweepKind, nil, next, jc.Cfg.JobMaxAttempts); err != nil {
		return fmt.Errorf("reschedule order auto-accept sweep: %w", err)
	}
	return nil
}

// releaseSellerEarnings moves a seller's payable for an order from pending
// to available earnings once it is accepted or auto-accepted (PLAN.md
// section 8, step 11). Mirrors handlers.Server.releaseSellerEarnings; it is
// a no-op when the order was never captured through the ledger (payments
// disabled, or the order predates Phase 5).
func releaseSellerEarnings(ctx context.Context, jc *jobContext, o *store.Order) {
	intent, err := jc.Store.LatestPaymentIntentForOrder(ctx, o.ID)
	if err != nil || intent.Status != services.PaymentSucceeded {
		return
	}
	payable := o.TotalMinorUnits - o.PlatformFeeMinorUnits
	if payable <= 0 {
		return
	}
	entries, err := ledger.EarningsReleased(o.ID, o.SellerID, payable, strings.ToLower(o.Currency))
	if err != nil {
		jc.Log.Error("build earnings-released ledger entries", "order_id", o.ID, "error", err)
		return
	}
	if _, err := jc.Store.PostLedgerEntries(ctx, entries); err != nil {
		jc.Log.Error("post earnings-released ledger entries", "order_id", o.ID, "error", err)
	}
}

// notifyOrderParty mirrors handlers.Server.notify: record the in-app
// notification, then enqueue best-effort email delivery for it. Duplicated
// rather than shared because job handlers live in package main and cannot
// depend on a *handlers.Server.
func notifyOrderParty(ctx context.Context, jc *jobContext, userID int64, kind, body, link string) {
	if _, err := jc.Store.CreateNotification(ctx, userID, kind, body, link); err != nil {
		jc.Log.Error("create notification", "error", err)
		return
	}
	if _, err := jc.Store.EnqueueJob(ctx, notificationEmailKind, map[string]any{
		"user_id": userID, "body": body, "link": link,
	}, time.Time{}, 3); err != nil {
		jc.Log.Error("enqueue notification email", "error", err)
	}
}
