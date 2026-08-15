package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/tayyebi/gig/config"
	"github.com/tayyebi/gig/providers"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// openIntegrationStore mirrors store's own testConfig/openTestStore helpers
// (unexported there, so re-declared here for this package's integration
// test); skips unless TEST_DATABASE_URL is set, same as `make test-integration`.
func openIntegrationStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database integration test")
	}
	st, err := store.Open(context.Background(), &config.Config{
		DatabaseURL:       dsn,
		DBMaxOpenConns:    4,
		DBMaxIdleConns:    2,
		DBConnMaxLifetime: time.Minute,
	})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// stubProvider implements providers.Provider with only ParseEvent wired up;
// the webhook-to-ledger job never calls the other methods (the HTTP-level
// signature verification already happened before the job was enqueued).
type stubProvider struct {
	name  string
	event providers.VerifiedEvent
}

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) CreatePayment(context.Context, providers.CreatePaymentInput) (providers.PaymentSession, error) {
	return providers.PaymentSession{}, fmt.Errorf("not implemented in stub")
}
func (s *stubProvider) Payment(context.Context, string) (providers.NormalizedPayment, error) {
	return providers.NormalizedPayment{}, fmt.Errorf("not implemented in stub")
}
func (s *stubProvider) Refund(context.Context, providers.RefundInput) (providers.RefundResult, error) {
	return providers.RefundResult{}, fmt.Errorf("not implemented in stub")
}
func (s *stubProvider) VerifyWebhook(context.Context, *http.Request, []byte) (providers.VerifiedEvent, error) {
	return providers.VerifiedEvent{}, fmt.Errorf("not implemented in stub")
}
func (s *stubProvider) ParseEvent(context.Context, []byte) (providers.VerifiedEvent, error) {
	return s.event, nil
}

// TestWebhookToLedgerCapturesPayment covers TODO.md's cross-cutting gate
// "no integration test yet for the webhook-to-ledger path": a
// payment.webhook_process job for a succeeded payment must move the order
// pending_payment -> paid -> in_progress and post balanced ledger entries,
// exactly once even if the same event is processed twice (the dedup guard
// checked in processPaymentWebhook before any state changes).
func TestWebhookToLedgerCapturesPayment(t *testing.T) {
	st := openIntegrationStore(t)
	ctx := context.Background()

	buyerID, err := st.CreateUser(ctx, fmt.Sprintf("buyer-%d@example.test", time.Now().UnixNano()), "hash", "Buyer", "en")
	if err != nil {
		t.Fatalf("CreateUser buyer: %v", err)
	}
	sellerID, err := st.CreateUser(ctx, fmt.Sprintf("seller-%d@example.test", time.Now().UnixNano()), "hash", "Seller", "en")
	if err != nil {
		t.Fatalf("CreateUser seller: %v", err)
	}
	var gigID int64
	if err := st.DB().QueryRowContext(ctx, `
		INSERT INTO gigs (seller_id, slug, title, status, moderation_state)
		VALUES ($1, $2, 'Test Gig', 'active', 'approved') RETURNING id`,
		sellerID, fmt.Sprintf("wh-gig-%d", time.Now().UnixNano())).Scan(&gigID); err != nil {
		t.Fatalf("insert gig: %v", err)
	}
	var orderID int64
	if err := st.DB().QueryRowContext(ctx, `
		INSERT INTO orders (buyer_id, seller_id, gig_id, status, subtotal_minor_units, platform_fee_minor_units, total_minor_units, currency)
		VALUES ($1, $2, $3, 'pending_payment', 900, 100, 1000, 'USD') RETURNING id`,
		buyerID, sellerID, gigID).Scan(&orderID); err != nil {
		t.Fatalf("insert order: %v", err)
	}

	intent, err := st.CreatePaymentIntent(ctx, orderID, "stripe", "card", 1000, "USD", fmt.Sprintf("idem-wh-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("CreatePaymentIntent: %v", err)
	}
	providerRef := fmt.Sprintf("cs_wh_%d", time.Now().UnixNano())
	if err := st.SetPaymentIntentSession(ctx, intent.ID, providerRef, "https://example.test/checkout", nil); err != nil {
		t.Fatalf("SetPaymentIntentSession: %v", err)
	}

	verified := providers.VerifiedEvent{
		Provider:    "stripe",
		EventID:     fmt.Sprintf("evt_wh_%d", time.Now().UnixNano()),
		EventType:   "checkout.session.completed",
		ProviderRef: providerRef,
		Payment: &providers.NormalizedPayment{
			ProviderRef: providerRef,
			ChargeRef:   "ch_wh_test",
			Status:      services.PaymentSucceeded,
			AmountMinor: 1000,
			Currency:    "usd",
		},
	}
	rawPayload := []byte(`{"id":"stub"}`)
	if _, _, err := st.InsertWebhookEvent(ctx, "stripe", verified.EventID, verified.EventType, providerRef, rawPayload, "hash"); err != nil {
		t.Fatalf("InsertWebhookEvent: %v", err)
	}

	jc := &jobContext{
		Store:     st,
		Log:       slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Providers: providers.NewRegistry(&stubProvider{name: "stripe", event: verified}),
	}
	payload, err := json.Marshal(paymentWebhookPayload{Provider: "stripe", EventID: verified.EventID})
	if err != nil {
		t.Fatalf("marshal job payload: %v", err)
	}
	job := store.Job{Kind: paymentWebhookProcessKind, Payload: payload}

	if err := processPaymentWebhook(ctx, jc, job); err != nil {
		t.Fatalf("processPaymentWebhook: %v", err)
	}

	order, err := st.GetOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if order.Status != services.OrderInProgress {
		t.Fatalf("order status after capture = %q, want %q", order.Status, services.OrderInProgress)
	}

	balance := sellerPendingBalance(t, st, sellerID)
	if balance != 900 {
		t.Fatalf("seller_pending balance = %d, want 900", balance)
	}

	// Re-processing the same event (a provider retry, or a second worker
	// picking up a re-enqueued job) must not double-post the ledger or
	// re-run the order transition.
	if err := processPaymentWebhook(ctx, jc, job); err != nil {
		t.Fatalf("processPaymentWebhook (replay): %v", err)
	}
	balanceAfterReplay := sellerPendingBalance(t, st, sellerID)
	if balanceAfterReplay != 900 {
		t.Fatalf("seller_pending balance after replay = %d, want 900 (no double posting)", balanceAfterReplay)
	}
}

func sellerPendingBalance(t *testing.T, st *store.Store, sellerID int64) int64 {
	t.Helper()
	balances, err := st.SellerBalances(context.Background(), sellerID)
	if err != nil {
		t.Fatalf("SellerBalances: %v", err)
	}
	for _, b := range balances {
		if b.Kind == "seller_pending" && b.Currency == "USD" {
			return b.BalanceMinor
		}
	}
	return 0
}
