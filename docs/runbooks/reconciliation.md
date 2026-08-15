# Reconciliation Runbook

Status: **DRAFT**, written from the current implementation (`payments.go`
`paymentReconcileSweep`, `/admin/payments`).

## What reconciles automatically

`payment.reconcile_sweep` runs on a fixed interval
(`PAYMENT_RECONCILE_INTERVAL`) and re-checks every payment intent still
`pending`/`processing` directly against its provider
(`store.ListStalePaymentIntents`). This catches:

- A webhook that was never delivered (network blip, provider outage).
- A webhook that arrived but failed to process and dead-lettered.
- BTCPay/EVM payments, which have no inbound webhook at all for EVM (it's
  polled by design) and a less reliable webhook path for BTCPay.

Nothing needs to be done manually for this class of drift — it self-heals
on the next sweep.

## What to check manually, on a regular cadence (weekly, or after any
incident)

1. **`/admin/payments`**: any dead-lettered `payment.webhook_process`
   jobs? Each one represents an event that failed repeatedly and needs a
   human look before retrying (retry button is right there, but don't
   retry blindly — read the job's last error first).
2. **Ledger balance sanity check**: `store.PlatformBalances` /
   `store.SellerBalances` should always reconcile to
   `sum(seller_pending) + sum(seller_available) + platform_revenue +
   reserves - refunds == sum(all captured payments) - sum(all refunds)`.
   There is no automated dashboard assertion for this cross-account
   identity today (only `ledger.Validate`'s per-posting balance check) —
   a genuine gap if the platform grows past a size where "look at the
   numbers" is enough. Consider a periodic job that runs this check and
   alerts on drift.
3. **Provider dashboard cross-check**: spot-check a sample of recent
   Stripe/BTCPay transactions against this platform's `payment_intents`
   table. This catches classes of bugs that ledger self-consistency checks
   can't (e.g. a webhook that was verified and processed but for the wrong
   amount because of a provider-side data issue).
4. **On-chain spot-check for EVM payments**: since there's no indexer,
   just the treasury address's own `Transfer` log history — periodically
   confirm the treasury's actual on-chain balance matches what the ledger
   believes has been captured minus what's been paid out.

## If reconciliation finds a discrepancy

1. Identify whether it's a missing capture (provider says paid, ledger
   doesn't show it) or a phantom capture (ledger shows paid, provider
   doesn't confirm it).
2. Missing capture: check if the intent is stuck `pending`/`processing` —
   if so, the next sweep should catch it; if it's already `failed` or
   `expired` incorrectly, this needs a manual `TransitionPaymentIntent`
   and a corrective ledger posting via `/admin/ledger/adjust`.
3. Phantom capture: treat as a security incident until proven otherwise —
   this would mean either a webhook signature was somehow accepted
   fraudulently, or a bug posted ledger entries without a real payment.
   Escalate per `docs/runbooks/incident.md`.
