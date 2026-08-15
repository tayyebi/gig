package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// insertTestUser and insertTestGig are minimal raw-SQL fixtures (this
// package has no shared fixture helpers yet) for tests that need an
// order or payout to hang a payment_intents/payouts row off of.
func insertTestUser(t *testing.T, st *Store, emailPrefix string) int64 {
	t.Helper()
	id, err := st.CreateUser(context.Background(), fmt.Sprintf("%s-%d@example.test", emailPrefix, time.Now().UnixNano()), "hash", "Test User", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return id
}

func insertTestGig(t *testing.T, st *Store, sellerID int64) int64 {
	t.Helper()
	var id int64
	err := st.db.QueryRowContext(context.Background(), `
		INSERT INTO gigs (seller_id, slug, title, status, moderation_state)
		VALUES ($1, $2, 'Test Gig', 'active', 'approved') RETURNING id`,
		sellerID, fmt.Sprintf("test-gig-%d", time.Now().UnixNano())).Scan(&id)
	if err != nil {
		t.Fatalf("insert gig: %v", err)
	}
	return id
}

func insertTestOrder(t *testing.T, st *Store, buyerID, sellerID, gigID int64, status string) int64 {
	t.Helper()
	var id int64
	err := st.db.QueryRowContext(context.Background(), `
		INSERT INTO orders (buyer_id, seller_id, gig_id, status, subtotal_minor_units, total_minor_units, currency)
		VALUES ($1, $2, $3, $4, 1000, 1000, 'USD') RETURNING id`,
		buyerID, sellerID, gigID, status).Scan(&id)
	if err != nil {
		t.Fatalf("insert order: %v", err)
	}
	return id
}

// TestListStalePaymentIntentsExpiry covers TODO.md Phase 8's "test expired
// payment sessions": a pending intent whose provider checkout session has
// aged past the reconciliation window must surface via
// ListStalePaymentIntents so the sweep can re-check and fail it, while a
// fresh intent (just created) must not.
func TestListStalePaymentIntentsExpiry(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	buyer := insertTestUser(t, st, "buyer")
	seller := insertTestUser(t, st, "seller")
	gig := insertTestGig(t, st, seller)

	staleOrder := insertTestOrder(t, st, buyer, seller, gig, "pending_payment")
	staleIntent, err := st.CreatePaymentIntent(ctx, staleOrder, "stripe", "card", 1000, "USD", fmt.Sprintf("idem-stale-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("CreatePaymentIntent (stale): %v", err)
	}
	if err := st.SetPaymentIntentSession(ctx, staleIntent.ID, "cs_stale_session", "https://example.test/checkout", nil); err != nil {
		t.Fatalf("SetPaymentIntentSession: %v", err)
	}
	// Backdate created_at directly; nothing in the store API exposes this,
	// and the sweep only ever looks at real clock age.
	if _, err := st.db.ExecContext(ctx, `UPDATE payment_intents SET created_at = now() - interval '2 hours' WHERE id = $1`, staleIntent.ID); err != nil {
		t.Fatalf("backdate stale intent: %v", err)
	}

	freshOrder := insertTestOrder(t, st, buyer, seller, gig, "pending_payment")
	freshIntent, err := st.CreatePaymentIntent(ctx, freshOrder, "stripe", "card", 1000, "USD", fmt.Sprintf("idem-fresh-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("CreatePaymentIntent (fresh): %v", err)
	}
	if err := st.SetPaymentIntentSession(ctx, freshIntent.ID, "cs_fresh_session", "https://example.test/checkout", nil); err != nil {
		t.Fatalf("SetPaymentIntentSession: %v", err)
	}

	stale, err := st.ListStalePaymentIntents(ctx, time.Hour, 500)
	if err != nil {
		t.Fatalf("ListStalePaymentIntents: %v", err)
	}
	var sawStale, sawFresh bool
	for _, p := range stale {
		if p.ID == staleIntent.ID {
			sawStale = true
		}
		if p.ID == freshIntent.ID {
			sawFresh = true
		}
	}
	if !sawStale {
		t.Errorf("stale intent %d not returned by ListStalePaymentIntents", staleIntent.ID)
	}
	if sawFresh {
		t.Errorf("fresh intent %d incorrectly returned as stale", freshIntent.ID)
	}
}

// TestConcurrentTransitionPayoutOnlyOneWinner covers "test concurrent
// acceptance/refund/payout attempts": two admins approving the same queued
// payout at once (a double-click, or two browser tabs) must not both
// succeed — TransitionPayout's WHERE ... AND status = $2 guard should let
// exactly one requester move it out of the 'queued' state.
func TestConcurrentTransitionPayoutOnlyOneWinner(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	seller := insertTestUser(t, st, "payout-seller")
	fingerprint := fmt.Sprintf("fp-%d", time.Now().UnixNano())
	walletID, err := st.CreatePendingWallet(ctx, seller, "base", "usdc", []byte("cipher"), fingerprint)
	if err != nil {
		t.Fatalf("CreatePendingWallet: %v", err)
	}
	if err := st.ConfirmWallet(ctx, walletID, 0); err != nil {
		t.Fatalf("ConfirmWallet: %v", err)
	}

	payoutID, err := st.CreatePayout(ctx, seller, walletID, 5000, "USD", "base", "usdc", "queued")
	if err != nil {
		t.Fatalf("CreatePayout: %v", err)
	}

	const admins = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < admins; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			reviewer := int64(n + 1)
			err := st.TransitionPayout(ctx, payoutID, "queued", "ready_for_manual_execution", &reviewer, "")
			if errors.Is(err, ErrNotFound) {
				return
			}
			if err != nil {
				t.Errorf("TransitionPayout: %v", err)
				return
			}
			mu.Lock()
			winners++
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("payout %d approved by %d concurrent admins, want exactly 1", payoutID, winners)
	}

	final, err := st.GetPayout(ctx, payoutID)
	if err != nil {
		t.Fatalf("GetPayout: %v", err)
	}
	if final.Status != "ready_for_manual_execution" {
		t.Fatalf("final payout status = %q, want ready_for_manual_execution", final.Status)
	}
}

// TestListPayoutsBySeller covers the seller-facing payout request/history
// page: it must return only the requesting seller's own payouts, not
// another seller's, most recent first.
func TestListPayoutsBySeller(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	sellerA := insertTestUser(t, st, "payout-list-a")
	sellerB := insertTestUser(t, st, "payout-list-b")
	fpA := fmt.Sprintf("fp-a-%d", time.Now().UnixNano())
	fpB := fmt.Sprintf("fp-b-%d", time.Now().UnixNano())
	walletA, err := st.CreatePendingWallet(ctx, sellerA, "base", "usdc", []byte("cipher"), fpA)
	if err != nil {
		t.Fatalf("CreatePendingWallet A: %v", err)
	}
	walletB, err := st.CreatePendingWallet(ctx, sellerB, "base", "usdc", []byte("cipher"), fpB)
	if err != nil {
		t.Fatalf("CreatePendingWallet B: %v", err)
	}

	payoutA1, err := st.CreatePayout(ctx, sellerA, walletA, 1000, "USD", "base", "usdc", PayoutQueued)
	if err != nil {
		t.Fatalf("CreatePayout A1: %v", err)
	}
	payoutA2, err := st.CreatePayout(ctx, sellerA, walletA, 2000, "USD", "base", "usdc", PayoutQueued)
	if err != nil {
		t.Fatalf("CreatePayout A2: %v", err)
	}
	if _, err := st.CreatePayout(ctx, sellerB, walletB, 3000, "USD", "base", "usdc", PayoutQueued); err != nil {
		t.Fatalf("CreatePayout B: %v", err)
	}

	got, err := st.ListPayoutsBySeller(ctx, sellerA, 10)
	if err != nil {
		t.Fatalf("ListPayoutsBySeller: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListPayoutsBySeller returned %d rows, want 2", len(got))
	}
	ids := map[int64]bool{got[0].ID: true, got[1].ID: true}
	if !ids[payoutA1] || !ids[payoutA2] {
		t.Fatalf("ListPayoutsBySeller returned %v, want %d and %d", ids, payoutA1, payoutA2)
	}
	for _, p := range got {
		if p.SellerID != sellerA {
			t.Fatalf("ListPayoutsBySeller leaked payout %d for seller %d into seller %d's list", p.ID, p.SellerID, sellerA)
		}
	}
}
