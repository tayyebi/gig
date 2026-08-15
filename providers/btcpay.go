package providers

import (
	"bytes"
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

// BTCPay talks to a self-hosted BTCPay Server's Greenfield REST API over
// net/http (PLAN.md requires a standard-library-only Go backend; there is no
// vendored BTCPay SDK). It uses BTCPay's per-order Invoices for buyer
// payment, matching the zero-JavaScript hosted-checkout requirement in
// PLAN.md section 9: the buyer is redirected to BTCPay's own hosted invoice
// page, which supports both on-chain Bitcoin and Lightning depending on how
// the store's wallets are configured.
type BTCPay struct {
	BaseURL    string // e.g. https://btcpay.example.com, no trailing slash
	APIKey     string // Greenfield API key with invoice + refund permissions on StoreID
	StoreID    string
	HTTPClient *http.Client
}

// NewBTCPay builds a BTCPay adapter. baseURL, apiKey, and storeID may be
// empty in dev/test environments where BTCPay is not configured; calls will
// fail clearly if used.
func NewBTCPay(baseURL, apiKey, storeID string) *BTCPay {
	return &BTCPay{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		StoreID:    storeID,
		HTTPClient: &http.Client{Timeout: 20 * time.Second},
	}
}

func (b *BTCPay) Name() string { return "btcpay" }

func (b *BTCPay) do(ctx context.Context, method, path string, body any, idempotencyKey string, out any) error {
	if b.BaseURL == "" || b.APIKey == "" || b.StoreID == "" {
		return fmt.Errorf("btcpay: not configured (base URL, API key, and store ID are all required)")
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("btcpay: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, b.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("btcpay: build request: %w", err)
	}
	req.Header.Set("Authorization", "token "+b.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// BTCPay's Greenfield API has no native idempotency-key header; the
	// adapter's own callers (checkout, refunds) already dedupe by storing
	// idempotencyKey on the local payment_intents/refunds rows, so a
	// duplicate call here just creates a new invoice/refund that the caller
	// discards. Kept as a parameter for interface symmetry with Stripe and to
	// tag the request for support correlation.
	if idempotencyKey != "" {
		req.Header.Set("X-Idempotency-Key", idempotencyKey)
	}
	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("btcpay: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("btcpay: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		return fmt.Errorf("btcpay: %s %s failed (%d): %s", method, path, resp.StatusCode, apiErr.Message)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("btcpay: decode response: %w", err)
		}
	}
	return nil
}

type btcpayInvoice struct {
	ID               string `json:"id"`
	StoreID          string `json:"storeId"`
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	Status           string `json:"status"`           // New, Processing, Settled, Expired, Invalid
	AdditionalStatus string `json:"additionalStatus"` // e.g. Unsettled, PaidPartial, PaidOver
	CreatedTime      int64  `json:"createdTime"`
	ExpirationTime   int64  `json:"expirationTime"`
	CheckoutLink     string `json:"checkoutLink"`
	Metadata         struct {
		OrderID string `json:"orderId"`
	} `json:"metadata"`
}

// CreatePayment creates a BTCPay invoice for one order and returns its
// hosted checkout URL. The buyer is redirected there; BTCPay redirects back
// to redirectURL, and the authoritative status still comes from the
// webhook, never from the return-URL request alone (mirrors Stripe).
func (b *BTCPay) CreatePayment(ctx context.Context, in CreatePaymentInput) (PaymentSession, error) {
	body := map[string]any{
		"amount":   btcpayAmountString(in.AmountMinor),
		"currency": strings.ToUpper(in.Currency),
		"metadata": map[string]any{
			"orderId":    strconv.FormatInt(in.OrderID, 10),
			"buyerEmail": in.BuyerEmail,
		},
		"checkout": map[string]any{
			"redirectURL":           in.SuccessURL,
			"redirectAutomatically": true,
			"expirationMinutes":     60,
			"requiresRefundEmail":   false,
			"speedPolicy":           "MediumSpeed",
		},
	}
	var out btcpayInvoice
	path := fmt.Sprintf("/api/v1/stores/%s/invoices", url.PathEscape(b.StoreID))
	if err := b.do(ctx, http.MethodPost, path, body, in.IdempotencyKey, &out); err != nil {
		return PaymentSession{}, err
	}
	var expires time.Time
	if out.ExpirationTime > 0 {
		expires = time.Unix(out.ExpirationTime, 0)
	}
	return PaymentSession{ProviderRef: out.ID, CheckoutURL: out.CheckoutLink, ExpiresAt: expires}, nil
}

// Payment fetches the current status of a BTCPay invoice by its ID.
func (b *BTCPay) Payment(ctx context.Context, providerRef string) (NormalizedPayment, error) {
	var out btcpayInvoice
	path := fmt.Sprintf("/api/v1/stores/%s/invoices/%s", url.PathEscape(b.StoreID), url.PathEscape(providerRef))
	if err := b.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return NormalizedPayment{}, err
	}
	amountMinor, _ := btcpayAmountToMinor(out.Amount)
	return NormalizedPayment{
		ProviderRef: out.ID,
		ChargeRef:   out.ID, // BTCPay has no separate charge concept; the invoice itself is refundable.
		Status:      normalizeInvoiceStatus(out.Status, out.AdditionalStatus),
		AmountMinor: amountMinor,
		Currency:    strings.ToLower(out.Currency),
	}, nil
}

// normalizeInvoiceStatus maps BTCPay's invoice status (and, for the
// underpayment/overpayment/partial-payment cases PLAN.md section 9 calls
// out, its additionalStatus) onto the platform's normalized vocabulary.
// PaidPartial (underpayment) is treated as still Processing rather than
// Succeeded: the risk policy here is that an order is only fulfillment-ready
// once BTCPay itself reports the invoice Settled.
func normalizeInvoiceStatus(status, additionalStatus string) string {
	switch status {
	case "New":
		return StatusPending
	case "Processing":
		return StatusProcessing
	case "Settled":
		return StatusSucceeded
	case "Expired":
		if additionalStatus == "PaidPartial" || additionalStatus == "PaidLate" {
			// Money arrived after expiry or was underpaid; this needs manual
			// reconciliation rather than being silently treated as a normal
			// expiry, but there is no separate normalized status for it yet,
			// so it is surfaced as Expired and left for admin review via the
			// reconciliation sweep and admin payment views.
			return StatusExpired
		}
		return StatusExpired
	case "Invalid":
		return StatusFailed
	default:
		return StatusPending
	}
}

// Refund issues a BTCPay refund against a settled invoice. BTCPay refunds
// are not instantaneous fund movements like Stripe's: creating a refund
// makes a claimable "pull payment" that the buyer (or, with an email on
// file, an automatic payout) must still settle on-chain or over Lightning,
// so the result is always reported as Processing rather than Succeeded —
// the reconciliation sweep and admin console are where a stuck refund
// becomes visible (PLAN.md section 12).
func (b *BTCPay) Refund(ctx context.Context, in RefundInput) (RefundResult, error) {
	body := map[string]any{
		"name":          "Order refund",
		"description":   in.Reason,
		"amount":        btcpayAmountString(in.AmountMinor),
		"currency":      strings.ToUpper(in.Currency),
		"refundVariant": "Custom",
	}
	var out struct {
		PullPaymentID string `json:"pullPaymentId"`
	}
	path := fmt.Sprintf("/api/v1/stores/%s/invoices/%s/refund", url.PathEscape(b.StoreID), url.PathEscape(in.ProviderRef))
	if err := b.do(ctx, http.MethodPost, path, body, in.IdempotencyKey, &out); err != nil {
		return RefundResult{}, err
	}
	ref := out.PullPaymentID
	if ref == "" {
		ref = in.ProviderRef
	}
	return RefundResult{ProviderRef: ref, Status: StatusProcessing}, nil
}

// btcpayWebhookTolerance rejects webhook deliveries whose timestamp is
// further from now than this. BTCPay's webhook payload carries its own
// "timestamp" field (unlike Stripe, which puts it in the signature header),
// so this is checked against the parsed body rather than the header.
const btcpayWebhookTolerance = 5 * time.Minute

// VerifyWebhook validates the BTCPay-Sig header, which is
// "sha256=" + hex(HMAC-SHA256(body, webhookSecret)), per BTCPay's documented
// webhook signing scheme, without depending on a BTCPay SDK.
func (b *BTCPay) VerifyWebhook(ctx context.Context, r *http.Request, body []byte) (VerifiedEvent, error) {
	secret := btcpayWebhookSecretFromContext(ctx)
	if secret == "" {
		return VerifiedEvent{}, fmt.Errorf("btcpay: no webhook secret configured")
	}
	header := r.Header.Get("BTCPay-Sig")
	sig, err := parseBTCPaySignatureHeader(header)
	if err != nil {
		return VerifiedEvent{}, err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return VerifiedEvent{}, fmt.Errorf("btcpay: webhook signature mismatch")
	}
	evt, err := b.parseEvent(body)
	if err != nil {
		return VerifiedEvent{}, err
	}
	if age := time.Since(time.Unix(evt.timestamp, 0)); evt.timestamp > 0 && (age > btcpayWebhookTolerance || age < -btcpayWebhookTolerance) {
		return VerifiedEvent{}, fmt.Errorf("btcpay: webhook timestamp outside tolerance")
	}
	return evt.VerifiedEvent, nil
}

// parsedBTCPayEvent wraps VerifiedEvent with the raw event timestamp, needed
// by VerifyWebhook's freshness check but not part of the normalized shape
// exposed to the rest of the codebase.
type parsedBTCPayEvent struct {
	VerifiedEvent
	timestamp int64
}

// ParseEvent decodes a BTCPay webhook payload without re-checking its
// signature, for use by the async processing job against the payload
// already persisted (and verified) at receipt time.
func (b *BTCPay) ParseEvent(ctx context.Context, body []byte) (VerifiedEvent, error) {
	pe, err := b.parseEvent(body)
	if err != nil {
		return VerifiedEvent{}, err
	}
	return pe.VerifiedEvent, nil
}

func (b *BTCPay) parseEvent(body []byte) (parsedBTCPayEvent, error) {
	var evt struct {
		DeliveryID string `json:"deliveryId"`
		Type       string `json:"type"`
		Timestamp  int64  `json:"timestamp"`
		StoreID    string `json:"storeId"`
		InvoiceID  string `json:"invoiceId"`
	}
	if err := json.Unmarshal(body, &evt); err != nil {
		return parsedBTCPayEvent{}, fmt.Errorf("btcpay: decode webhook payload: %w", err)
	}
	out := VerifiedEvent{Provider: "btcpay", EventID: evt.DeliveryID, EventType: evt.Type, ProviderRef: evt.InvoiceID, RawPayload: body}
	if status, ok := btcpayEventStatus(evt.Type); ok {
		out.Payment = &NormalizedPayment{
			ProviderRef: evt.InvoiceID,
			ChargeRef:   evt.InvoiceID,
			Status:      status,
		}
	}
	return parsedBTCPayEvent{VerifiedEvent: out, timestamp: evt.Timestamp}, nil
}

// btcpayEventStatus maps BTCPay's webhook event types onto the normalized
// payment status vocabulary. Event types this platform does not act on
// (e.g. InvoiceCreated, InvoicePaymentSettled for a single partial payment)
// return ok=false so the caller leaves Payment nil and just acks the event.
func btcpayEventStatus(eventType string) (status string, ok bool) {
	switch eventType {
	case "InvoiceSettled":
		return StatusSucceeded, true
	case "InvoiceProcessing":
		return StatusProcessing, true
	case "InvoiceExpired":
		return StatusExpired, true
	case "InvoiceInvalid":
		return StatusFailed, true
	default:
		return "", false
	}
}

func parseBTCPaySignatureHeader(header string) (string, error) {
	if header == "" {
		return "", fmt.Errorf("btcpay: missing BTCPay-Sig header")
	}
	sig, found := strings.CutPrefix(header, "sha256=")
	if !found || sig == "" {
		return "", fmt.Errorf("btcpay: malformed BTCPay-Sig header")
	}
	return sig, nil
}

type btcpayWebhookSecretKey struct{}

// WithBTCPayWebhookSecret attaches the webhook signing secret to a context
// for VerifyWebhook. It is passed per-call rather than stored on BTCPay so
// the same adapter instance can serve requests before config is fully
// loaded in tests, mirroring providers.WithWebhookSecret for Stripe.
func WithBTCPayWebhookSecret(ctx context.Context, secret string) context.Context {
	return context.WithValue(ctx, btcpayWebhookSecretKey{}, secret)
}

func btcpayWebhookSecretFromContext(ctx context.Context) string {
	s, _ := ctx.Value(btcpayWebhookSecretKey{}).(string)
	return s
}

// btcpayAmountString renders minor units (e.g. cents) as the decimal string
// BTCPay's invoice API expects for a fiat reference currency (e.g. "12.34"
// for 1234 cents). Only 2-decimal fiat currencies are supported, matching
// the rest of the codebase's money handling (services.CalculateOrderTotals,
// formatMoney).
func btcpayAmountString(amountMinor int64) string {
	neg := ""
	if amountMinor < 0 {
		neg = "-"
		amountMinor = -amountMinor
	}
	return fmt.Sprintf("%s%d.%02d", neg, amountMinor/100, amountMinor%100)
}

// btcpayAmountToMinor parses a BTCPay decimal amount string back into minor
// units, the inverse of btcpayAmountString.
func btcpayAmountToMinor(amount string) (int64, error) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return 0, nil
	}
	neg := false
	if strings.HasPrefix(amount, "-") {
		neg = true
		amount = amount[1:]
	}
	parts := strings.SplitN(amount, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("btcpay: parse amount %q: %w", amount, err)
	}
	var frac int64
	if len(parts) == 2 {
		fracStr := parts[1]
		if len(fracStr) > 2 {
			fracStr = fracStr[:2]
		}
		for len(fracStr) < 2 {
			fracStr += "0"
		}
		frac, err = strconv.ParseInt(fracStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("btcpay: parse amount %q: %w", amount, err)
		}
	}
	total := whole*100 + frac
	if neg {
		total = -total
	}
	return total, nil
}
