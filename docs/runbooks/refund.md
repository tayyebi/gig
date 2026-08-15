# Refund Runbook

Status: **DRAFT**, written from the current implementation
(`handlers/payments.go` `adminOrderRefund`).

## When to issue a refund

Per the proposed policy in `docs/phase0-decisions.md`: full refund at
admin discretion, within 14 days of `paid_at`, or any time before seller
delivery. There is no partial-refund capability today — it's full or
nothing.

## How to issue one

1. Go to `/admin/orders/{id}/payments`.
2. Confirm the order actually has a succeeded, refundable payment
   (`intent.Status == succeeded && intent.ChargeRef != ""` — the UI will
   say "This order has no refundable payment" otherwise).
3. Enter a reason (required — it's stored on the refund row and shown in
   the audit trail) and submit.
4. The system will reject a second refund attempt on the same order
   automatically (`idx_refunds_order_not_failed`,
   `store.GetRefundForOrder` pre-check) — if you see "This order already
   has a refund in progress or completed," check the order's existing
   refund status before assuming something is broken.

## What happens under the hood

1. The provider call (`provider.Refund`) is idempotency-keyed
   (`services.IdempotencyKey("refund:%d", orderID, 0)`), so even a network
   retry at the provider layer won't double-charge the seller's Stripe
   balance.
2. A `refunds` row is created (`store.CreateRefund`) and its status set
   from the provider's response.
3. If the provider reports the refund succeeded immediately, ledger
   entries are posted (`ledger.RefundIssued`) reversing the platform fee
   and seller payable, and (if the seller had already accepted the order)
   pulling the funds back out of `seller_available`.
4. **BTCPay refunds are not instant** — they create a pull payment the
   buyer must claim, so the refund status stays `processing` until the
   reconciliation sweep (or a manual admin re-check) confirms it settled.
   Don't be alarmed if a crypto refund doesn't show `succeeded`
   immediately.

## If the provider call fails

The admin UI shows "The refund could not be issued. Please try again." and
nothing is written to the `refunds` table (the provider call happens
before `CreateRefund`), so a failed attempt is safe to retry — it is not
counted as a duplicate. Check application logs for the underlying provider
error before retrying blindly.

## If a refund needs to be reversed (should essentially never happen)

There is no "un-refund" flow. If a refund was issued in error, this is a
manual ledger adjustment (`/admin/ledger/adjust`) plus a conversation with
the buyer about repayment — treat it as an incident, not routine
operations.
