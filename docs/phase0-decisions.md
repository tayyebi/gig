# Phase 0 Decisions — Proposed Defaults

Status: **PROPOSED**. These are not binding business or legal decisions —
they need sign-off from whoever owns the business, tax, and legal
relationship for this platform (PLAN.md Phase 0). What follows is a
consistent, self-coherent default derived from what the codebase already
assumes and implements, so Phase 0 has a concrete starting point instead of
an open question. Each item below states the current code default (if any)
and the proposal.

## Operating country and initial seller countries

- **Code default**: no country gate exists; `locale` defaults to `en`,
  currency defaults to `USD` (`config.go`, `services/orders.go`).
- **Proposal**: incorporate and operate from the United States; open seller
  onboarding first to US-resident sellers, expanding to EU/UK sellers once
  Stripe Connect cross-border payouts and any additional tax reporting
  (1099-K equivalents, EU VAT) are confirmed with counsel. Buyers: no
  country restriction at launch beyond sanctions screening (see Compliance
  checklist).

## Marketplace business model and fee model

- **Code default**: `platform_settings.platform_fee_bps` is seeded and
  admin-editable (`handlers/admin.go` `adminSettings`); the checkout total
  calculation (`services/orders.go`) already splits gross payment into
  seller payable and platform fee.
- **Proposal**: marketplace-of-record model — the platform is the seller of
  record for payment processing purposes (Stripe Connect Express handles
  this split automatically), takes a percentage-of-transaction fee from the
  seller side (not an added-on buyer fee), starting at a nominal rate
  (`platform_fee_bps` default already seeded; confirm the exact number with
  finance before launch).

## Currencies and tax responsibilities

- **Code default**: `USD`, 2-decimal minor units throughout the ledger and
  orders.
- **Proposal**: USD only at launch. Each seller is responsible for their own
  income tax reporting; the platform issues Stripe Connect's standard tax
  forms (1099-K where applicable) rather than building custom tax reporting.
  Sales/marketplace-facilitator tax (if applicable per US state) is out of
  scope for the initial build and must be reviewed with counsel before
  expanding beyond services exempt from that requirement.

## Seller-of-record responsibilities

- **Proposal**: the seller is the merchant of record for the underlying
  service; the platform is a payment facilitator and marketplace operator,
  not the merchant of record for tax/consumer-protection purposes. This
  should be stated explicitly in the Terms of Service (not yet drafted;
  legal must own that document, not this codebase).

## Stripe Connect account type (Express vs Custom)

- **Code default**: Express (`providers/stripe.go` onboarding link
  creation) — chosen because it pushes KYC/compliance UI and most
  compliance liability onto Stripe.
- **Proposal**: confirm Express. Revisit Custom only if the product needs
  more control over the seller onboarding UI than Express allows.

## BTCPay Server test environment

- **Code default**: `providers/btcpay.go` talks to any BTCPay Greenfield API
  endpoint via config (`BTCPAY_*` env vars); no environment is provisioned
  by this codebase (that's infrastructure, not code).
- **Action needed**: stand up a BTCPay Server instance (self-hosted or a
  hosted provider) pointed at Bitcoin testnet/signet before Phase 6
  features are exercised end-to-end. Not something this repository can do
  on its own — it's a hosting/ops action.

## Stablecoin network (Base vs Polygon)

- **Code default**: **both**, config-selected — `providers/evm.go` registers
  one adapter instance per chain (`evm-base`/`evm-polygon`), each active
  only when its RPC URL and treasury address are configured (already
  resolved this way during Phase 7 implementation).
- **Proposal**: confirm "both" as the answer, rather than picking one.

## Settlement asset and indexer/provider

- **Code default**: USDC and USDT, ERC-20 `Transfer` events read directly
  via `eth_getLogs`/`eth_blockNumber` against the configured `EVM_*_RPC_URL`
  (no third-party indexer dependency, per PLAN.md's stdlib-only constraint).
- **Proposal**: confirm USDC as the primary settlement asset (better
  regulatory clarity than USDT in most jurisdictions); keep USDT available
  but flagged for extra scrutiny/manual review in the admin console if
  volume grows.

## Crypto custody model and emergency-pause plan

- **Code default**: no custody — a single configured treasury address per
  chain receives funds directly (`config.EVMBaseTreasuryAddress` /
  `EVMPolygonTreasuryAddress`); there is no platform-held private key for
  seller payouts (`store/wallets.go`: payouts reach
  `ready_for_manual_execution` and an admin executes the transfer manually
  from treasury, recording the tx hash). Emergency pause exists:
  `platform_settings.payouts_paused`, `/admin/payouts/pause`.
- **Proposal**: confirm this "no custody, manual treasury execution" model
  for the initial launch — it avoids the platform ever holding seller funds
  in a hot wallet it can sign from programmatically, which is the biggest
  reducible risk. Revisit automated payout signing only after a proper
  custody/HSM review (see `docs/runbooks/treasury-custody.md`).

## Refund policy

- **Code default**: admin-only, full-refund-only action
  (`/admin/orders/{id}/refund`); no buyer-facing refund request flow;
  refunds are DB-deduplicated per order (`idx_refunds_order_not_failed`).
- **Proposal**: full refund available at admin discretion up to 14 days
  after `paid_at` (matching most consumer-protection default windows) or
  any time before seller delivery, whichever is later. Partial refunds are
  out of scope for this pass — a real gap if disputes commonly settle for
  partial amounts; flagged as a Phase 8+ follow-up if that turns out to be
  common. See `docs/runbooks/refund.md`.

## Escrow-like hold and auto-accept policy

- **Code default**: `config.AutoAcceptPeriod` — funds move to
  `seller_pending` on payment capture, buyer must explicitly accept (or the
  order auto-accepts after the configured period) before funds move to
  `seller_available` (`ledger.EarningsReleased`).
- **Proposal**: confirm a 3-day auto-accept window (already the seeded
  config default) as the standard hold period.

## Dispute policy

- **Code default**: buyer or seller can open a dispute with evidence
  uploads; an admin records a resolution decision
  (`/admin/disputes/{id}`); no automated adjudication (explicitly a
  PLAN.md non-goal).
- **Proposal**: disputes are reviewed within 2 business days of opening;
  resolution options are refund-buyer, release-to-seller, or partial
  (manual ledger adjustment, `/admin/ledger/adjust`). This should be
  reviewed by whoever handles trust & safety, not decided unilaterally in
  code.

## Payout policy

- **Code default**: seller-initiated payout requests
  (`/sell/payouts/request`), capped at available balance, high-value
  requests (`services.IsHighValue`, currently ≥ $2,000) routed to manual
  admin review; Stripe Connect payouts are automatic once
  `payouts_enabled`; wallet payouts require a confirmed wallet past its
  cooling-off period.
- **Proposal**: confirm the $2,000 manual-review threshold and the wallet
  cooldown (`WALLET_CHANGE_COOLDOWN`, currently config-driven) as launch
  defaults; revisit both after real transaction-volume data exists.

## Threat model and data classification

Produced as separate documents: `docs/threat-model.md` and
`docs/data-classification.md`.

## Consent, retention, and privacy policy draft (ops)

Drafted at `docs/privacy-policy-draft.md`. This is a starting point for
legal review, not a publishable policy.
