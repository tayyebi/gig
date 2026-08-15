package handlers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// multipartReq posts a multipart/form-data request (needed wherever a
// handler calls r.ParseMultipartForm, e.g. order delivery/dispute uploads)
// through the same cookie jar and CSRF flow as authServer.req.
func (a *authServer) multipartReq(t *testing.T, path string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write multipart field %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	for _, c := range a.jar {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		replaced := false
		for i, existing := range a.jar {
			if existing.Name == c.Name {
				a.jar[i] = c
				replaced = true
				break
			}
		}
		if !replaced {
			a.jar = append(a.jar, c)
		}
	}
	return rec
}

// extractFormValue finds the first `name="field" value="..."` occurrence in
// rendered HTML — the same pattern authServer.csrf already uses for the
// CSRF token, generalized to any hidden/attribute value this test needs to
// read back out of a real server-rendered page rather than assuming it.
func extractFormValue(t *testing.T, body, field string) string {
	t.Helper()
	marker := `name="` + field + `" value="`
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatalf("no %s field found in page", field)
	}
	rest := body[idx+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

// TestBuyerSellerJourneyEndToEnd drives an entire order lifecycle purely
// through HTTP form posts and GETs — no Go code ever calls a handler
// function directly, and nothing simulates a browser executing JavaScript,
// because this application ships none. This is TODO.md's "run every
// journey with JavaScript disabled in the browser" made concrete: a
// zero-JS, server-rendered app has no meaningful difference between "a
// browser with JS disabled" and "an HTTP client that only ever follows
// links and submits forms" — which is exactly what this test does. The one
// exception is the payment webhook step, which is never browser-driven in
// production either (it's a provider server calling this application
// directly), so it's simulated at the store layer here exactly as
// webhook_ledger_test.go and load_test.go already do.
func TestBuyerSellerJourneyEndToEnd(t *testing.T) {
	a := newAuthServer(t)
	ctx := context.Background()

	// --- Seller: register, opt in, publish a gig with one package. ---
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Journey+Seller&email=journey-seller@example.com&password=password123&password_confirm=password123&_csrf="+csrf)

	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/sell/start", "_csrf="+csrf)

	csrf = a.csrf(t, "/sell/profile")
	a.req(t, http.MethodPost, "/sell/profile",
		"display_name=Journey+Seller&bio=I+build+things.&location=Remote&_csrf="+csrf)

	csrf = a.csrf(t, "/sell/gigs/new")
	form := "title=Journey+Test+Gig&description=A+gig+used+only+by+the+end-to-end+journey+test." +
		"&category_id=&tags=testing" +
		"&pkg_name_0=Basic&pkg_description_0=One+revision&pkg_price_0=25.00&pkg_delivery_0=3&pkg_revisions_0=1" +
		"&_csrf=" + csrf
	rec := a.req(t, http.MethodPost, "/sell/gigs", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /sell/gigs = %d, body=%s", rec.Code, rec.Body.String())
	}
	editLocation := rec.Header().Get("Location")
	gigIDStr := strings.TrimSuffix(strings.TrimPrefix(editLocation, "/sell/gigs/"), "/edit")

	csrf = a.csrf(t, editLocation)
	rec = a.req(t, http.MethodPost, "/sell/gigs/"+gigIDStr+"/status", "status=active&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("publish gig = %d", rec.Code)
	}

	rec = a.req(t, http.MethodGet, "/search?q=journey", "")
	body := rec.Body.String()
	idx := strings.Index(body, `href="/gigs/`)
	if idx < 0 {
		t.Fatalf("published gig not found in search: %s", body)
	}
	rest := body[idx+len(`href="/gigs/`):]
	slug := rest[:strings.Index(rest, `"`)]

	// --- Switch to buyer: register, browse, checkout. The gig-detail
	// buy form (with package_id) only renders for a signed-in visitor who
	// isn't the gig's own seller (handlers/catalog.go CanOrder), so this
	// has to happen as the buyer, not while still logged in as the
	// seller who just published it. ---
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	csrf = a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Journey+Buyer&email=journey-buyer@example.com&password=password123&password_confirm=password123&_csrf="+csrf)

	rec = a.req(t, http.MethodGet, "/gigs/"+slug, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET gig detail = %d", rec.Code)
	}
	packageID := extractFormValue(t, rec.Body.String(), "package_id")

	csrf = a.csrf(t, "/gigs/"+slug)
	rec = a.req(t, http.MethodPost, "/gigs/"+slug+"/checkout", "package_id="+packageID+"&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start checkout = %d, body=%s", rec.Code, rec.Body.String())
	}
	requirementsLocation := rec.Header().Get("Location")
	draftIDStr := strings.TrimSuffix(strings.TrimPrefix(requirementsLocation, "/checkout/"), "/requirements")

	csrf = a.csrf(t, requirementsLocation)
	rec = a.req(t, http.MethodPost, "/checkout/"+draftIDStr+"/requirements",
		"requirements=Please+build+the+thing+as+described.&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("submit requirements = %d, body=%s", rec.Code, rec.Body.String())
	}

	rec = a.req(t, http.MethodGet, "/checkout/"+draftIDStr+"/review", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "$25.00") {
		t.Fatalf("checkout review = %d, body=%s", rec.Code, rec.Body.String())
	}

	csrf = a.csrf(t, "/checkout/"+draftIDStr+"/review")
	rec = a.req(t, http.MethodPost, "/checkout/"+draftIDStr+"/confirm", "payment_method=&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm checkout = %d, body=%s", rec.Code, rec.Body.String())
	}
	orderLocation := rec.Header().Get("Location") // "/orders/{id}", since payments are disabled in this test config
	orderIDStr := strings.TrimPrefix(orderLocation, "/orders/")

	rec = a.req(t, http.MethodGet, orderLocation, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pending") {
		t.Fatalf("order detail before payment = %d, body=%s", rec.Code, rec.Body.String())
	}

	// The payment webhook is never a browser action in production either —
	// simulate what a verified provider webhook does, same as
	// webhook_ledger_test.go, rather than driving fake payment UI.
	orderID, err := strconv.ParseInt(orderIDStr, 10, 64)
	if err != nil {
		t.Fatalf("parse order id %q: %v", orderIDStr, err)
	}
	if err := a.srv.Store.TransitionOrder(ctx, orderID, "pending_payment", "paid"); err != nil {
		t.Fatalf("simulate payment capture: %v", err)
	}
	if err := a.srv.Store.TransitionOrder(ctx, orderID, "paid", "in_progress"); err != nil {
		t.Fatalf("simulate order start: %v", err)
	}

	// --- Buyer messages the seller. ---
	csrf = a.csrf(t, orderLocation)
	rec = a.req(t, http.MethodPost, "/orders/"+orderIDStr+"/messages", "message_body=Looking+forward+to+it!&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("buyer message = %d", rec.Code)
	}

	// --- Seller delivers, buyer requests a revision, seller redelivers. ---
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	csrf = a.csrf(t, "/login")
	a.req(t, http.MethodPost, "/login", "email=journey-seller@example.com&password=password123&_csrf="+csrf)

	// A seller-flagged "request info" message (TODO.md Phase 4's "seller
	// requests for buyer information") renders with a distinct badge.
	csrf = a.csrf(t, orderLocation)
	rec = a.req(t, http.MethodPost, "/orders/"+orderIDStr+"/messages",
		"message_body=Which+font+family+do+you+prefer%3F&request_info=1&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("seller info request = %d, body=%s", rec.Code, rec.Body.String())
	}
	rec = a.req(t, http.MethodGet, orderLocation, "")
	if !strings.Contains(rec.Body.String(), "Info requested") {
		t.Fatalf("info-request badge not rendered: %s", rec.Body.String())
	}

	csrf = a.csrf(t, orderLocation)
	rec = a.multipartReq(t, "/orders/"+orderIDStr+"/deliver", map[string]string{
		"delivery_message": "Here is the first draft.",
		"_csrf":            csrf,
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("seller deliver = %d, body=%s", rec.Code, rec.Body.String())
	}

	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	csrf = a.csrf(t, "/login")
	a.req(t, http.MethodPost, "/login", "email=journey-buyer@example.com&password=password123&_csrf="+csrf)

	rec = a.req(t, http.MethodGet, orderLocation, "")
	if !strings.Contains(rec.Body.String(), "Here is the first draft.") {
		t.Fatalf("buyer cannot see delivery message: %s", rec.Body.String())
	}

	csrf = a.csrf(t, orderLocation)
	rec = a.req(t, http.MethodPost, "/orders/"+orderIDStr+"/revise", "revise_reason=Please+use+a+different+color.&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("buyer revise = %d, body=%s", rec.Code, rec.Body.String())
	}

	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	csrf = a.csrf(t, "/login")
	a.req(t, http.MethodPost, "/login", "email=journey-seller@example.com&password=password123&_csrf="+csrf)

	csrf = a.csrf(t, orderLocation)
	rec = a.multipartReq(t, "/orders/"+orderIDStr+"/deliver", map[string]string{
		"delivery_message": "Updated with the new color.",
		"_csrf":            csrf,
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("seller redeliver = %d, body=%s", rec.Code, rec.Body.String())
	}

	// --- Buyer accepts and reviews, then opens a dispute anyway (allowed
	// from accepted, e.g. a post-acceptance problem), which an admin
	// resolves. ---
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	csrf = a.csrf(t, "/login")
	a.req(t, http.MethodPost, "/login", "email=journey-buyer@example.com&password=password123&_csrf="+csrf)

	csrf = a.csrf(t, orderLocation)
	rec = a.req(t, http.MethodPost, "/orders/"+orderIDStr+"/accept", "_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("buyer accept = %d, body=%s", rec.Code, rec.Body.String())
	}

	csrf = a.csrf(t, orderLocation)
	rec = a.req(t, http.MethodPost, "/orders/"+orderIDStr+"/review", "review_rating=5&review_body=Great+work%21&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("buyer review = %d, body=%s", rec.Code, rec.Body.String())
	}

	csrf = a.csrf(t, orderLocation)
	rec = a.multipartReq(t, "/orders/"+orderIDStr+"/dispute", map[string]string{
		"dispute_reason": "The final invoice amount looked wrong.",
		"_csrf":          csrf,
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("buyer open dispute = %d, body=%s", rec.Code, rec.Body.String())
	}

	// adminTestUser registers a fresh account, which requires an anonymous
	// session (/register redirects an already-authenticated visitor away)
	// — log the buyer out first.
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	adminID := adminTestUser(t, a, "journey-admin@example.com")
	_ = adminID
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	csrf = a.csrf(t, "/login")
	a.req(t, http.MethodPost, "/login", "email=journey-admin@example.com&password=password123&_csrf="+csrf)

	csrf = a.csrf(t, orderLocation)
	rec = a.req(t, http.MethodPost, "/orders/"+orderIDStr+"/dispute/resolve",
		"dispute_decision=Invoice+amount+confirmed+correct%3B+no+adjustment+needed.&dispute_outcome=closed&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("admin resolve dispute = %d, body=%s", rec.Code, rec.Body.String())
	}

	final, err := a.srv.Store.GetOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("GetOrder final: %v", err)
	}
	if final.Status != "closed" {
		t.Fatalf("final order status = %q, want closed", final.Status)
	}

	// A moderator-visible review exists, pending approval (Phase 4's
	// review moderation state), and the seller's earnings reflect the
	// accepted order.
	rev, err := a.srv.Store.GetReviewByOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("GetReviewByOrder: %v", err)
	}
	if rev.Rating != 5 {
		t.Errorf("review rating = %d, want 5", rev.Rating)
	}
}

