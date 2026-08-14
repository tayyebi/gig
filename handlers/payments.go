package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	htmltemplate "html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tayyebi/gig/components"
	"github.com/tayyebi/gig/providers"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// pageWithRawBody wraps a pre-escaped HTML fragment in a PageData, for the
// small hand-built admin payment views below.
func pageWithRawBody(title, body string) components.PageData {
	return components.PageData{Title: title, Body: htmltemplate.HTML(body)}
}

// paymentWebhookJobKind is the job kind the worker registers a handler for
// (main.paymentWebhookProcessKind); duplicated as a string literal since
// handlers cannot import package main.
const paymentWebhookJobKind = "payment.webhook_process"

// startPayment creates a payment intent for a freshly confirmed order and
// redirects the buyer to the provider's hosted checkout page. If no provider
// is configured (payments not operationally enabled), the order is left in
// pending_payment and the buyer is sent to the order page with a notice,
// same as before Phase 5.
func (s *Server) startPayment(w http.ResponseWriter, r *http.Request, u *store.User, order *store.Order) {
	if !s.Cfg.PaymentsEnabled || s.Provider == nil {
		s.flashNotice(r, "Payment collection is not enabled yet; this order is on hold pending payment.")
		http.Redirect(w, r, fmt.Sprintf("/orders/%d", order.ID), http.StatusSeeOther)
		return
	}

	currency := strings.ToLower(order.Currency)
	key := services.IdempotencyKey("checkout", order.ID, 0)
	intent, err := s.Store.CreatePaymentIntent(r.Context(), order.ID, s.Provider.Name(), "card", order.TotalMinorUnits, currency, key)
	if errors.Is(err, store.ErrDuplicateIdempotencyKey) {
		intent, err = s.Store.GetPaymentIntentByIdempotencyKey(r.Context(), key)
	}
	if err != nil {
		s.renderError(w, err)
		return
	}

	if intent.CheckoutURL != "" && intent.Status == services.PaymentPending {
		http.Redirect(w, r, intent.CheckoutURL, http.StatusSeeOther)
		return
	}

	in := providers.CreatePaymentInput{
		OrderID:        order.ID,
		AmountMinor:    order.TotalMinorUnits,
		Currency:       currency,
		Description:    fmt.Sprintf("Order #%d", order.ID),
		IdempotencyKey: intent.IdempotencyKey,
		SuccessURL:     fmt.Sprintf("%s/orders/%d/pay/return", s.Cfg.BaseURL, order.ID),
		CancelURL:      fmt.Sprintf("%s/orders/%d/pay/cancel", s.Cfg.BaseURL, order.ID),
		BuyerEmail:     u.Email,
	}
	session, err := s.Provider.CreatePayment(r.Context(), in)
	if err != nil {
		s.Log.Error("create provider payment session", "error", err, "order_id", order.ID)
		s.flashError(r, "We could not start payment for this order. Please try again.")
		http.Redirect(w, r, fmt.Sprintf("/orders/%d", order.ID), http.StatusSeeOther)
		return
	}

	var expiresAt *time.Time
	if !session.ExpiresAt.IsZero() {
		expiresAt = &session.ExpiresAt
	}
	if err := s.Store.SetPaymentIntentSession(r.Context(), intent.ID, session.ProviderRef, session.CheckoutURL, expiresAt); err != nil {
		s.renderError(w, err)
		return
	}

	s.audit(r.Context(), &u.ID, r, "payment.intent_created", "payment_intent", strconv.FormatInt(intent.ID, 10),
		map[string]any{"order_id": order.ID, "provider": s.Provider.Name(), "amount_minor_units": order.TotalMinorUnits})
	http.Redirect(w, r, session.CheckoutURL, http.StatusSeeOther)
}

// paymentReturn is where the provider redirects the buyer back after hosted
// checkout. The order's status is never advanced from this request; only
// the verified webhook does that. This just sends the buyer to the order
// page, where the status reflects whatever the webhook has (or hasn't yet)
// processed.
func (s *Server) paymentReturn(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.flashNotice(r, "Thanks! We're confirming your payment now.")
	http.Redirect(w, r, fmt.Sprintf("/orders/%d", id), http.StatusSeeOther)
}

func (s *Server) paymentCancelReturn(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.flashError(r, "Checkout was canceled. You can try paying again from this order.")
	http.Redirect(w, r, fmt.Sprintf("/orders/%d", id), http.StatusSeeOther)
}

// StripeWebhook receives and verifies Stripe webhook deliveries. It does the
// minimum synchronous work — verify the signature, dedupe-insert the raw
// event — then acks immediately and enqueues the actual processing as a job,
// so a slow or failing side effect never causes Stripe to see a timeout and
// retry storms. This handler is registered outside the session/CSRF chain
// (root server.go) since it is a server-to-server call with no session.
func (s *Server) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	if s.Provider == nil {
		http.Error(w, "payments not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := providers.WithWebhookSecret(r.Context(), s.Cfg.StripeWebhookSecret)
	evt, err := s.Provider.VerifyWebhook(ctx, r, body)
	if err != nil {
		s.Log.Warn("stripe webhook verification failed", "error", err)
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	_, inserted, err := s.Store.InsertWebhookEvent(r.Context(), evt.Provider, evt.EventID, evt.EventType, body, hash)
	if err != nil {
		s.Log.Error("insert webhook event", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if inserted {
		if _, err := s.Store.EnqueueJob(r.Context(), paymentWebhookJobKind, map[string]any{
			"provider": evt.Provider, "event_id": evt.EventID,
		}, time.Time{}, 8); err != nil {
			s.Log.Error("enqueue webhook processing job", "error", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// adminPayments and adminOrderPayments are minimal read-only inspection
// views (PLAN.md section 15's admin payment search/timeline requirement).
// They render a small hand-built fragment rather than a full component,
// escaping every value, since a dedicated admin console template is out of
// scope for this pass.
func (s *Server) adminPayments(w http.ResponseWriter, r *http.Request) {
	balances, err := s.Store.PlatformBalances(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}
	var sb strings.Builder
	sb.WriteString("<section class=\"container\"><h1>Platform balances</h1>")
	if len(balances) == 0 {
		sb.WriteString("<p>No ledger activity yet.</p>")
	} else {
		sb.WriteString("<table><thead><tr><th scope=\"col\">Account</th><th scope=\"col\">Currency</th><th scope=\"col\">Balance</th></tr></thead><tbody>")
		for _, b := range balances {
			currency := strings.ToUpper(b.Currency)
			sb.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td></tr>",
				html.EscapeString(b.Kind), html.EscapeString(currency), html.EscapeString(formatMoney(b.BalanceMinor, currency))))
		}
		sb.WriteString("</tbody></table>")
	}
	sb.WriteString("<p><a href=\"/admin\">Back to admin</a></p></section>")
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - payments", sb.String()))
}

func (s *Server) adminOrderPayments(w http.ResponseWriter, r *http.Request) {
	orderID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	intent, err := s.Store.LatestPaymentIntentForOrder(r.Context(), orderID)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, http.StatusOK, pageWithRawBody("Admin - order payments",
			fmt.Sprintf("<section class=\"container\"><h1>Order #%d payments</h1><p>No payment intent yet.</p></section>", orderID)))
		return
	}
	if err != nil {
		s.renderError(w, err)
		return
	}
	currency := strings.ToUpper(intent.Currency)
	body := fmt.Sprintf(`<section class="container"><h1>Order #%d payments</h1>
<dl>
<dt>Provider</dt><dd>%s</dd>
<dt>Provider reference</dt><dd>%s</dd>
<dt>Status</dt><dd>%s</dd>
<dt>Amount</dt><dd>%s</dd>
</dl></section>`, orderID, html.EscapeString(intent.Provider), html.EscapeString(intent.ProviderRef),
		html.EscapeString(intent.Status), html.EscapeString(formatMoney(intent.AmountMinor, currency)))
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - order payments", body))
}
