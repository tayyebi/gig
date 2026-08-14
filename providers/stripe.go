package providers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stripe talks to the Stripe REST API directly over net/http (PLAN.md
// requires a standard-library-only Go backend; there is no vendored Stripe
// SDK). It uses Stripe Checkout Sessions for buyer payment, matching the
// zero-JavaScript hosted-checkout requirement in PLAN.md section 9.
type Stripe struct {
	SecretKey  string
	HTTPClient *http.Client
	APIBase    string // overridable in tests
}

// NewStripe builds a Stripe adapter. secretKey may be empty in dev/test
// environments where payments are disabled; calls will fail clearly if used.
func NewStripe(secretKey string) *Stripe {
	return &Stripe{
		SecretKey:  secretKey,
		HTTPClient: &http.Client{Timeout: 20 * time.Second},
		APIBase:    "https://api.stripe.com/v1",
	}
}

func (sp *Stripe) Name() string { return "stripe" }

func (sp *Stripe) do(ctx context.Context, method, path string, form url.Values, idempotencyKey string, out any) error {
	if sp.SecretKey == "" {
		return fmt.Errorf("stripe: no API key configured")
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, sp.APIBase+path, body)
	if err != nil {
		return fmt.Errorf("stripe: build request: %w", err)
	}
	req.SetBasicAuth(sp.SecretKey, "")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := sp.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("stripe: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("stripe: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		return fmt.Errorf("stripe: %s %s failed (%d): %s", method, path, resp.StatusCode, apiErr.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("stripe: decode response: %w", err)
		}
	}
	return nil
}

type stripeCheckoutSession struct {
	ID            string `json:"id"`
	URL           string `json:"url"`
	Status        string `json:"status"`
	PaymentStatus string `json:"payment_status"`
	PaymentIntent string `json:"payment_intent"`
	ExpiresAt     int64  `json:"expires_at"`
	AmountTotal   int64  `json:"amount_total"`
	Currency      string `json:"currency"`
}

// CreatePayment creates a Stripe Checkout Session in payment mode and
// returns its hosted URL. The buyer is redirected there; Stripe redirects
// back to SuccessURL/CancelURL, and the authoritative status still comes
// from the webhook, never from the return-URL request alone.
func (sp *Stripe) CreatePayment(ctx context.Context, in CreatePaymentInput) (PaymentSession, error) {
	form := url.Values{}
	form.Set("mode", "payment")
	form.Set("success_url", in.SuccessURL)
	form.Set("cancel_url", in.CancelURL)
	form.Set("client_reference_id", strconv.FormatInt(in.OrderID, 10))
	if in.BuyerEmail != "" {
		form.Set("customer_email", in.BuyerEmail)
	}
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", in.Currency)
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(in.AmountMinor, 10))
	form.Set("line_items[0][price_data][product_data][name]", in.Description)
	form.Set("payment_intent_data[metadata][order_id]", strconv.FormatInt(in.OrderID, 10))

	var out stripeCheckoutSession
	if err := sp.do(ctx, http.MethodPost, "/checkout/sessions", form, in.IdempotencyKey, &out); err != nil {
		return PaymentSession{}, err
	}
	var expires time.Time
	if out.ExpiresAt > 0 {
		expires = time.Unix(out.ExpiresAt, 0)
	}
	return PaymentSession{ProviderRef: out.ID, CheckoutURL: out.URL, ExpiresAt: expires}, nil
}

// Payment fetches the current status of a Checkout Session by its ID.
func (sp *Stripe) Payment(ctx context.Context, providerRef string) (NormalizedPayment, error) {
	var out stripeCheckoutSession
	if err := sp.do(ctx, http.MethodGet, "/checkout/sessions/"+url.PathEscape(providerRef), nil, "", &out); err != nil {
		return NormalizedPayment{}, err
	}
	return NormalizedPayment{
		ProviderRef: out.ID,
		Status:      normalizeCheckoutSessionStatus(out.Status, out.PaymentStatus),
		AmountMinor: out.AmountTotal,
		Currency:    out.Currency,
	}, nil
}

func normalizeCheckoutSessionStatus(sessionStatus, paymentStatus string) string {
	switch sessionStatus {
	case "expired":
		return StatusExpired
	case "open":
		return StatusPending
	case "complete":
		if paymentStatus == "paid" || paymentStatus == "no_payment_required" {
			return StatusSucceeded
		}
		return StatusProcessing
	default:
		return StatusPending
	}
}

type stripeRefund struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Refund issues a refund against the PaymentIntent behind a Checkout
// Session (ProviderRef here is the PaymentIntent ID, resolved by the caller
// from the stored payment intent's provider metadata).
func (sp *Stripe) Refund(ctx context.Context, in RefundInput) (RefundResult, error) {
	form := url.Values{}
	form.Set("payment_intent", in.ProviderRef)
	if in.AmountMinor > 0 {
		form.Set("amount", strconv.FormatInt(in.AmountMinor, 10))
	}
	if in.Reason != "" {
		form.Set("metadata[reason]", in.Reason)
	}
	var out stripeRefund
	if err := sp.do(ctx, http.MethodPost, "/refunds", form, in.IdempotencyKey, &out); err != nil {
		return RefundResult{}, err
	}
	status := StatusProcessing
	switch out.Status {
	case "succeeded":
		status = StatusSucceeded
	case "failed", "canceled":
		status = StatusFailed
	}
	return RefundResult{ProviderRef: out.ID, Status: status}, nil
}

// stripeWebhookTolerance rejects webhook deliveries whose timestamp is
// further from now than this, guarding against replay of a captured
// request (Stripe's own recommended default).
const stripeWebhookTolerance = 5 * time.Minute

// VerifyWebhook validates the Stripe-Signature header per Stripe's documented
// scheme (HMAC-SHA256 over "{timestamp}.{payload}", constant-time compared,
// with a timestamp freshness check) without depending on the Stripe SDK.
func (sp *Stripe) VerifyWebhook(ctx context.Context, r *http.Request, body []byte) (VerifiedEvent, error) {
	secret := stripeWebhookSecretFromContext(ctx)
	if secret == "" {
		return VerifiedEvent{}, fmt.Errorf("stripe: no webhook secret configured")
	}
	header := r.Header.Get("Stripe-Signature")
	ts, sig, err := parseStripeSignatureHeader(header)
	if err != nil {
		return VerifiedEvent{}, err
	}
	if age := time.Since(time.Unix(ts, 0)); age > stripeWebhookTolerance || age < -stripeWebhookTolerance {
		return VerifiedEvent{}, fmt.Errorf("stripe: webhook timestamp outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.%s", ts, body)))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return VerifiedEvent{}, fmt.Errorf("stripe: webhook signature mismatch")
	}
	return sp.ParseEvent(ctx, body)
}

// ParseEvent decodes a Stripe event payload without re-checking its
// signature, for use by the async processing job against the payload
// already persisted (and verified) at receipt time.
func (sp *Stripe) ParseEvent(ctx context.Context, body []byte) (VerifiedEvent, error) {
	var evt struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &evt); err != nil {
		return VerifiedEvent{}, fmt.Errorf("stripe: decode webhook payload: %w", err)
	}

	var obj struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		PaymentStatus string `json:"payment_status"`
		AmountTotal   int64  `json:"amount_total"`
		Amount        int64  `json:"amount"`
		Currency      string `json:"currency"`
	}
	_ = json.Unmarshal(evt.Data.Object, &obj)

	out := VerifiedEvent{Provider: "stripe", EventID: evt.ID, EventType: evt.Type, ProviderRef: obj.ID, RawPayload: body}
	switch evt.Type {
	case "checkout.session.completed", "checkout.session.expired", "checkout.session.async_payment_failed", "checkout.session.async_payment_succeeded":
		out.Payment = &NormalizedPayment{
			ProviderRef: obj.ID,
			Status:      normalizeCheckoutSessionStatus(sessionStatusForEventType(evt.Type, obj.Status), obj.PaymentStatus),
			AmountMinor: obj.AmountTotal,
			Currency:    obj.Currency,
		}
	}
	return out, nil
}

// sessionStatusForEventType fills in the session-level status Stripe's async
// payment webhooks don't always echo back on the object itself.
func sessionStatusForEventType(eventType, status string) string {
	switch eventType {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		return "complete"
	case "checkout.session.expired":
		return "expired"
	default:
		return status
	}
}

func parseStripeSignatureHeader(header string) (ts int64, v1 string, err error) {
	if header == "" {
		return 0, "", fmt.Errorf("stripe: missing Stripe-Signature header")
	}
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts, err = strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return 0, "", fmt.Errorf("stripe: invalid signature timestamp: %w", err)
			}
		case "v1":
			v1 = kv[1]
		}
	}
	if ts == 0 || v1 == "" {
		return 0, "", fmt.Errorf("stripe: malformed Stripe-Signature header")
	}
	return ts, v1, nil
}

type stripeWebhookSecretKey struct{}

// WithWebhookSecret attaches the webhook signing secret to a context for
// VerifyWebhook. It is passed per-call rather than stored on Stripe so the
// same adapter instance can serve requests before config is fully loaded in
// tests.
func WithWebhookSecret(ctx context.Context, secret string) context.Context {
	return context.WithValue(ctx, stripeWebhookSecretKey{}, secret)
}

func stripeWebhookSecretFromContext(ctx context.Context) string {
	s, _ := ctx.Value(stripeWebhookSecretKey{}).(string)
	return s
}
