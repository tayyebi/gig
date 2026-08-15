package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// TestConcurrentLoadSearchGigDetailAndOrderDetail covers TODO.md's "run
// load tests on search, checkout, and messaging". A real load test against
// deployed infrastructure (concurrent users, network latency, connection
// pool exhaustion under production traffic) needs a running deployment
// this test suite doesn't have — but the read paths that dominate real
// traffic (search, a gig's checkout-adjacent detail page, an order's
// message thread) are exercised here with concurrent goroutines hitting
// the real handler stack (routing, session/auth middleware, database
// queries) in-process, which catches the class of bug a real load test
// would catch first: a data race, a connection-pool deadlock, or a query
// that silently assumes single-threaded access. It is not a substitute for
// a production load test, only the automatable slice of it.
func TestConcurrentLoadSearchGigDetailAndOrderDetail(t *testing.T) {
	a := newAuthServer(t)
	ctx := context.Background()

	// Register the buyer through HTTP (not store.CreateUser) so a.jar ends
	// up holding a real session cookie for this exact user — /orders/{id}
	// is buyer/seller/admin-gated, so a session belonging to some other
	// user would 404 every order-detail request below.
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Load+Buyer&email=load-buyer@example.com&password=password123&password_confirm=password123&_csrf="+csrf)
	buyer, err := a.srv.Store.GetUserByEmail(ctx, "load-buyer@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail buyer: %v", err)
	}
	buyerID := buyer.ID
	cookies := append([]*http.Cookie{}, a.jar...)

	sellerID, err := a.srv.Store.CreateUser(ctx, "load-seller@example.com", "hash", "Load Seller", "en")
	if err != nil {
		t.Fatalf("CreateUser seller: %v", err)
	}
	if err := a.srv.Store.AddRole(ctx, sellerID, store.RoleSeller); err != nil {
		t.Fatalf("AddRole seller: %v", err)
	}
	gig, err := a.srv.Store.CreateGig(ctx, sellerID, nil, "Load Test Gig", "A gig used only for the concurrency load test.")
	if err != nil {
		t.Fatalf("CreateGig: %v", err)
	}
	if err := a.srv.Store.ReplaceGigPackages(ctx, gig.ID, []store.GigPackage{
		{Tier: store.TierBasic, Name: "Basic", Description: "Basic tier", PriceMinorUnits: 2500, Currency: "USD", DeliveryDays: 2, Revisions: 1},
	}); err != nil {
		t.Fatalf("ReplaceGigPackages: %v", err)
	}
	if err := a.srv.Store.SetGigStatus(ctx, gig.ID, sellerID, store.GigActive); err != nil {
		t.Fatalf("SetGigStatus: %v", err)
	}

	order, err := a.srv.Store.CreateOrderFromDraft(ctx, 0, buyerID, sellerID, gig.ID, "", []store.OrderItemInput{
		{Kind: "package", Name: "Basic", PriceMinorUnits: 2500, Currency: "USD"},
	}, 2500, 250, 2750, "USD", 1)
	if err != nil {
		t.Fatalf("CreateOrderFromDraft: %v", err)
	}
	if err := a.srv.Store.TransitionOrder(ctx, order.ID, services.OrderPendingPayment, services.OrderPaid); err != nil {
		t.Fatalf("TransitionOrder to paid: %v", err)
	}
	if err := a.srv.Store.TransitionOrder(ctx, order.ID, services.OrderPaid, services.OrderInProgress); err != nil {
		t.Fatalf("TransitionOrder to in_progress: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := a.srv.Store.CreateOrderMessage(ctx, order.ID, sellerID, fmt.Sprintf("Update %d on your order.", i), false); err != nil {
			t.Fatalf("CreateOrderMessage: %v", err)
		}
	}

	// cookies (captured above right after registering the buyer) is reused
	// read-only by every goroutine below, since a.jar itself is not
	// goroutine-safe to mutate concurrently.
	do := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		a.handler.ServeHTTP(rec, req)
		return rec.Code
	}

	paths := []string{
		"/search?q=load",
		"/browse",
		"/gigs/" + gig.Slug,
		fmt.Sprintf("/orders/%d", order.ID),
	}

	const concurrency = 25
	var wg sync.WaitGroup
	errs := make(chan string, concurrency*len(paths))
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, p := range paths {
				if code := do(http.MethodGet, p); code != http.StatusOK {
					errs <- fmt.Sprintf("%s = %d, want 200", p, code)
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}
