package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tayyebi/gig/ledger"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// paymentWebhookProcessKind must match handlers.paymentWebhookJobKind: the
// HTTP handler enqueues it, the worker processes it.
const paymentWebhookProcessKind = "payment.webhook_process"

// paymentReconcileSweepKind periodically re-checks in-flight payment intents
// against the provider directly, to catch a webhook Stripe never managed to
// deliver (PLAN.md section 12, "Reconcile internal payment records...").
const paymentReconcileSweepKind = "payment.reconcile_sweep"

// registerPaymentJobs wires webhook processing and the recurring
// reconciliation sweep. Both are no-ops (log and return) when no provider is
// configured, so the worker still starts cleanly in dev/test.
func registerPaymentJobs(q *jobQueue, jc *jobContext) error {
	q.Register(paymentWebhookProcessKind, processPaymentWebhook)
	q.Register(paymentReconcileSweepKind, paymentReconcileSweep)
	if jc.Provider == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := q.st.EnqueueJob(ctx, paymentReconcileSweepKind, nil, time.Now().Add(jc.Cfg.PaymentReconcileInterval), jc.Cfg.JobMaxAttempts); err != nil {
		return fmt.Errorf("seed payment reconcile sweep: %w", err)
	}
	return nil
}

type paymentWebhookPayload struct {
	Provider string `json:"provider"`
	EventID  string `json:"event_id"`
}

// processPaymentWebhook re-derives the normalized event from the payload
// persisted at receipt time, applies it to the matching payment intent and
// order, and posts the corresponding ledger entries. Returning an error
// leaves the webhook event unmarked so the job queue's retry/backoff tries
// again; nothing here is destructive to retry.
func processPaymentWebhook(ctx context.Context, jc *jobContext, job store.Job) error {
	if jc.Provider == nil {
		return fmt.Errorf("payment webhook received but no provider is configured")
	}
	var payload paymentWebhookPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("decode webhook job payload: %w", err)
	}
	event, err := jc.Store.GetWebhookEventByProviderEventID(ctx, payload.Provider, payload.EventID)
	if err != nil {
		return fmt.Errorf("load webhook event: %w", err)
	}
	if event.Status == "processed" {
		return nil // already handled by a prior attempt; nothing left to do
	}

	evt, err := jc.Provider.ParseEvent(ctx, event.Payload)
	if err != nil {
		return fmt.Errorf("parse webhook payload: %w", err)
	}
	if evt.Account != nil {
		if err := jc.Store.SetStripeAccountCapabilities(ctx, evt.Account.AccountID, evt.Account.ChargesEnabled, evt.Account.PayoutsEnabled); err != nil {
			return fmt.Errorf("update seller connect account: %w", err)
		}
		return jc.Store.MarkWebhookProcessed(ctx, event.ID)
	}
	if evt.Payment == nil {
		// An event type we don't act on. Ack it.
		return jc.Store.MarkWebhookProcessed(ctx, event.ID)
	}

	intent, err := jc.Store.GetPaymentIntentByProviderRef(ctx, evt.Provider, evt.ProviderRef)
	if errors.Is(err, store.ErrNotFound) {
		jc.Log.Warn("webhook for unknown payment intent", "provider", evt.Provider, "provider_ref", evt.ProviderRef)
		return jc.Store.MarkWebhookProcessed(ctx, event.ID)
	}
	if err != nil {
		return fmt.Errorf("load payment intent: %w", err)
	}

	if err := jc.Store.RecordPaymentAttempt(ctx, intent.ID, evt.Payment.Status, evt.Payment.FailureCode, evt.Payment.FailureReason, ""); err != nil {
		return fmt.Errorf("record payment attempt: %w", err)
	}
	if evt.Payment.ChargeRef != "" && evt.Payment.ChargeRef != intent.ChargeRef {
		if err := jc.Store.SetPaymentIntentChargeRef(ctx, intent.ID, evt.Payment.ChargeRef); err != nil {
			return fmt.Errorf("set payment intent charge ref: %w", err)
		}
		intent.ChargeRef = evt.Payment.ChargeRef
	}

	if services.CanTransitionPayment(intent.Status, evt.Payment.Status) {
		if err := jc.Store.TransitionPaymentIntent(ctx, intent.ID, intent.Status, evt.Payment.Status); err != nil {
			return fmt.Errorf("transition payment intent: %w", err)
		}
	}

	if evt.Payment.Status == services.PaymentSucceeded {
		if err := capturePaymentForOrder(ctx, jc, intent); err != nil {
			return err
		}
	} else if evt.Payment.Status == services.PaymentFailed || evt.Payment.Status == services.PaymentExpired {
		if err := failPaymentForOrder(ctx, jc, intent); err != nil {
			return err
		}
	}

	return jc.Store.MarkWebhookProcessed(ctx, event.ID)
}

// capturePaymentForOrder moves an order from pending_payment to paid to
// in_progress and posts the ledger entries for the captured funds. It is
// idempotent: if the order has already left pending_payment (a duplicate or
// out-of-order webhook), the transition is skipped rather than erroring, but
// the caller must still ensure ledger postings are not duplicated — guarded
// here by only posting when the pending_payment -> paid transition actually
// happened.
func capturePaymentForOrder(ctx context.Context, jc *jobContext, intent *store.PaymentIntent) error {
	order, err := jc.Store.GetOrder(ctx, intent.OrderID)
	if err != nil {
		return fmt.Errorf("load order for payment capture: %w", err)
	}
	if order.Status != services.OrderPendingPayment {
		jc.Log.Info("payment succeeded for order already past pending_payment; skipping duplicate capture", "order_id", order.ID, "status", order.Status)
		return nil
	}
	if err := jc.Store.TransitionOrder(ctx, order.ID, services.OrderPendingPayment, services.OrderPaid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil // raced with another delivery of the same webhook
		}
		return fmt.Errorf("transition order to paid: %w", err)
	}

	entries, err := ledger.PaymentCaptured(order.ID, order.SellerID, order.TotalMinorUnits, order.PlatformFeeMinorUnits, order.Currency)
	if err != nil {
		return fmt.Errorf("build capture ledger entries: %w", err)
	}
	if _, err := jc.Store.PostLedgerEntries(ctx, entries); err != nil {
		return fmt.Errorf("post capture ledger entries: %w", err)
	}

	if err := jc.Store.TransitionOrder(ctx, order.ID, services.OrderPaid, services.OrderInProgress); err != nil {
		return fmt.Errorf("transition order to in_progress: %w", err)
	}

	notifyOrderParty(ctx, jc, order.BuyerID, "payment.succeeded", fmt.Sprintf("Payment received for order #%d", order.ID), fmt.Sprintf("/orders/%d", order.ID))
	notifyOrderParty(ctx, jc, order.SellerID, "order.started", fmt.Sprintf("Order #%d is paid and ready to start", order.ID), fmt.Sprintf("/orders/%d", order.ID))
	return nil
}

// failPaymentForOrder moves an order to payment_failed so the buyer can
// retry checkout with a fresh payment intent.
func failPaymentForOrder(ctx context.Context, jc *jobContext, intent *store.PaymentIntent) error {
	order, err := jc.Store.GetOrder(ctx, intent.OrderID)
	if err != nil {
		return fmt.Errorf("load order for payment failure: %w", err)
	}
	if order.Status != services.OrderPendingPayment {
		return nil
	}
	if err := jc.Store.TransitionOrder(ctx, order.ID, services.OrderPendingPayment, services.OrderPaymentFailed); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("transition order to payment_failed: %w", err)
	}
	notifyOrderParty(ctx, jc, order.BuyerID, "payment.failed", fmt.Sprintf("Payment for order #%d did not go through", order.ID), fmt.Sprintf("/orders/%d", order.ID))
	return nil
}

// paymentReconcileSweep re-checks every payment intent still pending or
// processing directly against the provider, in case its webhook was never
// delivered, then reschedules itself.
func paymentReconcileSweep(ctx context.Context, jc *jobContext, _ store.Job) error {
	if jc.Provider != nil {
		intents, err := jc.Store.ListStalePaymentIntents(ctx, jc.Cfg.PaymentReconcileInterval, 100)
		if err != nil {
			return fmt.Errorf("list stale payment intents: %w", err)
		}
		for _, intent := range intents {
			payment, err := jc.Provider.Payment(ctx, intent.ProviderRef)
			if err != nil {
				jc.Log.Warn("reconcile: fetch provider payment failed", "intent_id", intent.ID, "error", err)
				continue
			}
			if payment.ChargeRef != "" && payment.ChargeRef != intent.ChargeRef {
				if err := jc.Store.SetPaymentIntentChargeRef(ctx, intent.ID, payment.ChargeRef); err != nil {
					jc.Log.Warn("reconcile: set charge ref failed", "intent_id", intent.ID, "error", err)
				} else {
					intent.ChargeRef = payment.ChargeRef
				}
			}
			if !services.CanTransitionPayment(intent.Status, payment.Status) {
				continue
			}
			if err := jc.Store.TransitionPaymentIntent(ctx, intent.ID, intent.Status, payment.Status); err != nil {
				jc.Log.Warn("reconcile: transition payment intent failed", "intent_id", intent.ID, "error", err)
				continue
			}
			switch payment.Status {
			case services.PaymentSucceeded:
				if err := capturePaymentForOrder(ctx, jc, &intent); err != nil {
					jc.Log.Error("reconcile: capture payment failed", "intent_id", intent.ID, "error", err)
				}
			case services.PaymentFailed, services.PaymentExpired:
				if err := failPaymentForOrder(ctx, jc, &intent); err != nil {
					jc.Log.Error("reconcile: fail payment failed", "intent_id", intent.ID, "error", err)
				}
			}
			jc.Log.Info("reconcile: payment intent status corrected via direct provider check", "intent_id", intent.ID, "status", payment.Status)
		}
	}

	next := time.Now().Add(jc.Cfg.PaymentReconcileInterval)
	if _, err := jc.Store.EnqueueJob(ctx, paymentReconcileSweepKind, nil, next, jc.Cfg.JobMaxAttempts); err != nil {
		return fmt.Errorf("reschedule payment reconcile sweep: %w", err)
	}
	return nil
}
