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
	"github.com/tayyebi/gig/ledger"
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

// paymentMethodProvider maps the payment method selected on the checkout
// review step to the provider name that handles it. "card" -> Stripe;
// "bitcoin" and "lightning" both route to BTCPay, which offers both rails
// from the same hosted invoice depending on the store's wallet configuration
// (PLAN.md section 9).
func paymentMethodProvider(method string) string {
	switch method {
	case "bitcoin", "lightning":
		return "btcpay"
	default:
		return "stripe"
	}
}

// startPayment creates a payment intent for a freshly confirmed order and
// redirects the buyer to the chosen provider's hosted checkout page. If no
// provider is configured at all (payments not operationally enabled), or
// the requested method's provider specifically is not configured, the order
// is left in pending_payment and the buyer is sent to the order page with a
// notice, same as before Phase 5.
func (s *Server) startPayment(w http.ResponseWriter, r *http.Request, u *store.User, order *store.Order, method string) {
	if !s.Cfg.PaymentsEnabled || s.Providers.Empty() {
		s.flashNotice(r, "Payment collection is not enabled yet; this order is on hold pending payment.")
		http.Redirect(w, r, fmt.Sprintf("/orders/%d", order.ID), http.StatusSeeOther)
		return
	}
	if method == "" {
		method = "card"
	}
	provider, ok := s.Providers.Get(paymentMethodProvider(method))
	if !ok {
		s.flashError(r, "That payment method is not available right now. Please choose another.")
		http.Redirect(w, r, fmt.Sprintf("/orders/%d", order.ID), http.StatusSeeOther)
		return
	}

	currency := strings.ToLower(order.Currency)
	key := services.IdempotencyKey("checkout", order.ID, 0)
	intent, err := s.Store.CreatePaymentIntent(r.Context(), order.ID, provider.Name(), method, order.TotalMinorUnits, currency, key)
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
	session, err := provider.CreatePayment(r.Context(), in)
	if err != nil {
		s.Log.Error("create provider payment session", "error", err, "order_id", order.ID, "provider", provider.Name())
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
		map[string]any{"order_id": order.ID, "provider": provider.Name(), "amount_minor_units": order.TotalMinorUnits})
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

// btcpayInvoiceStatus is a lightweight, zero-JavaScript polling page for a
// BTCPay invoice: it meta-refreshes every few seconds while the payment is
// still pending or processing (an on-chain payment can take a while to
// reach BTCPay's configured confirmation speed policy), and forwards the
// buyer to the order page once the webhook has resolved it one way or the
// other (PLAN.md section 9, "status refresh via meta-refresh page").
func (s *Server) btcpayInvoiceStatus(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	intent, err := s.Store.LatestPaymentIntentForOrder(r.Context(), access.Order.ID)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/orders/%d", access.Order.ID), http.StatusSeeOther)
		return
	}
	if intent.Provider != "btcpay" || intent.Status == services.PaymentSucceeded ||
		intent.Status == services.PaymentFailed || intent.Status == services.PaymentExpired || intent.Status == services.PaymentCanceled {
		http.Redirect(w, r, fmt.Sprintf("/orders/%d", access.Order.ID), http.StatusSeeOther)
		return
	}

	statusText := "waiting for payment"
	if intent.Status == services.PaymentProcessing {
		statusText = "payment detected, waiting for confirmation"
	}
	body := fmt.Sprintf(`<section class="container">
<h1>Bitcoin / Lightning payment</h1>
<p>Status: %s</p>
<p class="help">This page refreshes automatically. Once BTCPay confirms your payment, order #%d will move to in progress.</p>
<p><a href="%s">Continue to your Bitcoin/Lightning invoice</a></p>
<p><a href="/orders/%d">Back to order</a></p>
</section>`, html.EscapeString(statusText), access.Order.ID, html.EscapeString(intent.CheckoutURL), access.Order.ID)

	p := pageWithRawBody("Bitcoin / Lightning payment", body)
	p.MetaRefresh = 5
	s.render(w, r, http.StatusOK, p)
}

// stripeProvider narrows the registered "stripe" provider to the Stripe
// adapter for Connect onboarding, which is Stripe-specific and not part of
// the generic Provider interface used for payment collection.
func (s *Server) stripeProvider() (*providers.Stripe, bool) {
	p, ok := s.Providers.Get("stripe")
	if !ok {
		return nil, false
	}
	sp, ok := p.(*providers.Stripe)
	return sp, ok
}

// btcpayProvider narrows the registered "btcpay" provider to the BTCPay
// adapter, for the webhook handler below.
func (s *Server) btcpayProvider() (*providers.BTCPay, bool) {
	p, ok := s.Providers.Get("btcpay")
	if !ok {
		return nil, false
	}
	bp, ok := p.(*providers.BTCPay)
	return bp, ok
}

// sellOnboardingStripeStart creates (if needed) a Stripe Express account for
// the signed-in seller and redirects them to Stripe-hosted onboarding.
func (s *Server) sellOnboardingStripeStart(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	sp, ok := s.stripeProvider()
	if !ok {
		s.flashError(r, "Payouts are not enabled yet.")
		http.Redirect(w, r, "/sell", http.StatusSeeOther)
		return
	}
	onboarding, err := s.Store.GetSellerOnboarding(r.Context(), u.ID)
	if err != nil {
		s.renderError(w, err)
		return
	}
	accountID := onboarding.StripeAccountID
	if accountID == "" {
		accountID, err = sp.CreateConnectAccount(r.Context(), u.Email)
		if err != nil {
			s.Log.Error("create stripe connect account", "error", err, "user_id", u.ID)
			s.flashError(r, "We could not start Stripe onboarding. Please try again.")
			http.Redirect(w, r, "/sell", http.StatusSeeOther)
			return
		}
		if err := s.Store.SetStripeAccount(r.Context(), u.ID, accountID); err != nil {
			s.renderError(w, err)
			return
		}
		s.audit(r.Context(), &u.ID, r, "seller.stripe_account_created", "user", strconv.FormatInt(u.ID, 10), map[string]any{"stripe_account_id": accountID})
	}

	link, err := sp.CreateOnboardingLink(r.Context(), accountID,
		s.Cfg.BaseURL+"/sell/onboarding/stripe/return", s.Cfg.BaseURL+"/sell/onboarding/stripe/refresh")
	if err != nil {
		s.Log.Error("create stripe onboarding link", "error", err, "user_id", u.ID)
		s.flashError(r, "We could not open Stripe onboarding. Please try again.")
		http.Redirect(w, r, "/sell", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, link, http.StatusSeeOther)
}

// sellOnboardingStripeRefresh is Stripe's refresh_url: the onboarding link
// expired or was invalid, so a fresh one is generated the same way a first
// visit would.
func (s *Server) sellOnboardingStripeRefresh(w http.ResponseWriter, r *http.Request) {
	s.sellOnboardingStripeStart(w, r)
}

// sellOnboardingStripeReturn is Stripe's return_url after the hosted
// onboarding flow. The seller's capability state is never trusted from this
// request alone — it is authoritatively updated by the account.updated
// webhook — but a best-effort direct status check here lets the dashboard
// reflect completed onboarding immediately instead of waiting on webhook
// delivery.
func (s *Server) sellOnboardingStripeReturn(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	if sp, ok := s.stripeProvider(); ok {
		if onboarding, err := s.Store.GetSellerOnboarding(r.Context(), u.ID); err == nil && onboarding.StripeAccountID != "" {
			if status, err := sp.ConnectAccountStatus(r.Context(), onboarding.StripeAccountID); err == nil {
				_ = s.Store.SetStripeAccountCapabilities(r.Context(), status.AccountID, status.ChargesEnabled, status.PayoutsEnabled)
			}
		}
	}
	s.flashNotice(r, "Thanks! We're confirming your Stripe account status.")
	http.Redirect(w, r, "/sell", http.StatusSeeOther)
}

// StripeWebhook receives and verifies Stripe webhook deliveries. It does the
// minimum synchronous work — verify the signature, dedupe-insert the raw
// event — then acks immediately and enqueues the actual processing as a job,
// so a slow or failing side effect never causes Stripe to see a timeout and
// retry storms. This handler is registered outside the session/CSRF chain
// (root server.go) since it is a server-to-server call with no session.
func (s *Server) StripeWebhook(w http.ResponseWriter, r *http.Request) {
	sp, ok := s.stripeProvider()
	if !ok {
		http.Error(w, "payments not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := providers.WithWebhookSecret(r.Context(), s.Cfg.StripeWebhookSecret)
	evt, err := sp.VerifyWebhook(ctx, r, body)
	if err != nil {
		s.Log.Warn("stripe webhook verification failed", "error", err)
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}
	s.receiveWebhookEvent(w, r, body, evt)
}

// BTCPayWebhook receives and verifies BTCPay invoice webhook deliveries. It
// follows the same verify-dedupe-enqueue-and-ack pattern as StripeWebhook,
// registered outside the session/CSRF chain since it is a server-to-server
// call with no session (PLAN.md section 9: "process invoice and settlement
// webhooks idempotently").
func (s *Server) BTCPayWebhook(w http.ResponseWriter, r *http.Request) {
	bp, ok := s.btcpayProvider()
	if !ok {
		http.Error(w, "payments not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := providers.WithBTCPayWebhookSecret(r.Context(), s.Cfg.BTCPayWebhookSecret)
	evt, err := bp.VerifyWebhook(ctx, r, body)
	if err != nil {
		s.Log.Warn("btcpay webhook verification failed", "error", err)
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}
	s.receiveWebhookEvent(w, r, body, evt)
}

// receiveWebhookEvent does the minimum synchronous work shared by every
// provider webhook endpoint — dedupe-insert the raw, already-verified event
// — then acks immediately and enqueues the actual processing as a job, so a
// slow or failing side effect never causes the provider to see a timeout
// and retry storms.
func (s *Server) receiveWebhookEvent(w http.ResponseWriter, r *http.Request, body []byte, evt providers.VerifiedEvent) {
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
	deadJobs, err := s.Store.ListJobsByKindAndStatus(r.Context(), paymentWebhookJobKind, store.JobStatusDead, 50)
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

	sb.WriteString("<h2>Reconciliation exceptions</h2>")
	if len(deadJobs) == 0 {
		sb.WriteString("<p>No dead-lettered webhook jobs.</p>")
	} else {
		sb.WriteString("<p>These webhook events exhausted their retries and were never applied; they need manual review.</p>")
		sb.WriteString("<table><thead><tr><th scope=\"col\">Job ID</th><th scope=\"col\">Attempts</th><th scope=\"col\">Last error</th><th scope=\"col\">Updated</th></tr></thead><tbody>")
		for _, j := range deadJobs {
			lastError := ""
			if j.LastError != nil {
				lastError = *j.LastError
			}
			sb.WriteString(fmt.Sprintf("<tr><td>%d</td><td>%d/%d</td><td>%s</td><td>%s</td></tr>",
				j.ID, j.Attempts, j.MaxAttempts, html.EscapeString(lastError), html.EscapeString(j.UpdatedAt.Format("2006-01-02 15:04"))))
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
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<section class="container"><h1>Order #%d payments</h1>
<dl>
<dt>Provider</dt><dd>%s</dd>
<dt>Provider reference</dt><dd>%s</dd>
<dt>Status</dt><dd>%s</dd>
<dt>Amount</dt><dd>%s</dd>
</dl>`, orderID, html.EscapeString(intent.Provider), html.EscapeString(intent.ProviderRef),
		html.EscapeString(intent.Status), html.EscapeString(formatMoney(intent.AmountMinor, currency))))
	if intent.Status == services.PaymentSucceeded && intent.ChargeRef != "" {
		sb.WriteString(fmt.Sprintf(`<h2>Refund</h2>
<p class="help">Issues a full refund of %s through %s. There is no partial-refund support yet.</p>
<form method="post" action="/admin/orders/%d/refund" novalidate>
%s
<label for="reason">Reason</label>
<input id="reason" name="reason" type="text" required maxlength="500">
<button class="btn" type="submit">Refund order</button>
</form>`, html.EscapeString(formatMoney(intent.AmountMinor, currency)), html.EscapeString(intent.Provider), orderID, csrfInputHTML(s.csrfFor(r))))
	}
	sb.WriteString(`</section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - order payments", sb.String()))
}

func csrfInputHTML(token string) string {
	return fmt.Sprintf(`<input type="hidden" name="_csrf" value="%s">`, html.EscapeString(token))
}

// adminOrderRefund issues a full refund for an order's succeeded payment
// through the provider, then posts the corresponding ledger entries. Only a
// full refund of the original captured amount is supported; there is no
// partial-refund UI yet.
func (s *Server) adminOrderRefund(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	orderID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	if reason == "" {
		s.flashError(r, "A refund reason is required.")
		http.Redirect(w, r, fmt.Sprintf("/admin/orders/%d/payments", orderID), http.StatusSeeOther)
		return
	}

	order, err := s.Store.GetOrder(r.Context(), orderID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	intent, err := s.Store.LatestPaymentIntentForOrder(r.Context(), orderID)
	if err != nil || intent.Status != services.PaymentSucceeded || intent.ChargeRef == "" {
		s.flashError(r, "This order has no refundable payment.")
		http.Redirect(w, r, fmt.Sprintf("/admin/orders/%d/payments", orderID), http.StatusSeeOther)
		return
	}
	provider, ok := s.Providers.Get(intent.Provider)
	if !ok {
		s.flashError(r, "Payments are not enabled.")
		http.Redirect(w, r, fmt.Sprintf("/admin/orders/%d/payments", orderID), http.StatusSeeOther)
		return
	}

	amount := order.TotalMinorUnits
	result, err := provider.Refund(r.Context(), providers.RefundInput{
		ProviderRef:    intent.ChargeRef,
		AmountMinor:    amount,
		Currency:       intent.Currency,
		Reason:         reason,
		IdempotencyKey: services.IdempotencyKey(fmt.Sprintf("refund:%d", orderID), orderID, 0),
	})
	if err != nil {
		s.Log.Error("issue refund", "error", err, "order_id", orderID)
		s.flashError(r, "The refund could not be issued. Please try again.")
		http.Redirect(w, r, fmt.Sprintf("/admin/orders/%d/payments", orderID), http.StatusSeeOther)
		return
	}

	refundID, err := s.Store.CreateRefund(r.Context(), orderID, intent.ID, &admin.ID, reason, amount, intent.Currency)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.SetRefundStatus(r.Context(), refundID, result.Status, result.ProviderRef); err != nil {
		s.renderError(w, err)
		return
	}

	if result.Status == services.PaymentSucceeded {
		feeRefund := order.PlatformFeeMinorUnits
		payableRefund := amount - feeRefund
		sellerFundsAvailable := order.AcceptedAt != nil
		entries, err := ledger.RefundIssued(order.ID, order.SellerID, amount, feeRefund, payableRefund, sellerFundsAvailable, strings.ToLower(intent.Currency))
		if err != nil {
			s.Log.Error("build refund ledger entries", "error", err, "order_id", orderID)
		} else if _, err := s.Store.PostLedgerEntries(r.Context(), entries); err != nil {
			s.Log.Error("post refund ledger entries", "error", err, "order_id", orderID)
		}
		s.notify(r.Context(), order.BuyerID, "payment.refunded", fmt.Sprintf("Order #%d was refunded", order.ID), fmt.Sprintf("/orders/%d", order.ID))
	}

	s.audit(r.Context(), &admin.ID, r, "payment.refund_issued", "order", strconv.FormatInt(orderID, 10),
		map[string]any{"amount_minor_units": amount, "status": result.Status, "provider_ref": result.ProviderRef})
	s.flashNotice(r, "Refund issued.")
	http.Redirect(w, r, fmt.Sprintf("/admin/orders/%d/payments", orderID), http.StatusSeeOther)
}
