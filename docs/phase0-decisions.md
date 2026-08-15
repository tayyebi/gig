# Phase 0 Decisions — Ratified

Status: **RATIFIED** by the project owner on 2026-08-15, in-session. This
supersedes the earlier proposed-defaults draft; the items below are the
actual Phase 0 decisions for this platform, not defaults awaiting
sign-off.

## Operating country and initial seller countries

**Decision**: no country restriction. This is a global platform — buyers
and sellers may onboard from any country not subject to sanctions
screening (see Compliance checklist), rather than launching restricted to
a single incorporation jurisdiction. `locale`/currency defaults already
reflect a global-first posture in code (`config.go`).

Operationally this means: no jurisdiction-specific tax facilitator logic
is built into checkout (VAT, sales tax, etc.) — each seller is
responsible for understanding and meeting their own local tax obligations
(see "Currencies and tax responsibilities" below). Country-specific legal
certification remains explicitly out of scope for the initial build per
PLAN.md's non-goals, consistent with operating globally rather than
gating by jurisdiction.

## Marketplace business model and fee model

**Decision (ratified as proposed)**: marketplace-of-record model — the
platform is the seller of record for payment processing purposes (Stripe
Connect Express handles this split automatically), takes a
percentage-of-transaction fee from the seller side (not an added-on buyer
fee). The exact `platform_fee_bps` value (seeded and admin-editable via
`/admin/settings`) is a finance decision to tune post-launch, not an
architectural one — the mechanism is ratified, the number is not fixed.

## Currencies and tax responsibilities

**Decision**: base settlement currency is **USD** for fiat, **USDT** for
the primary stablecoin rail (both already the ledger's minor-unit
convention and an already-configured EVM asset). This is a global,
not country-restricted, currency choice — USD/USDT function as the
platform's stable unit of account regardless of where a buyer or seller
is located, rather than tying the platform to one country's currency
regime.

Each seller remains responsible for their own tax reporting in their own
jurisdiction; the platform is not built as a tax withholding or
remittance agent for any specific country's regime, consistent with
operating globally rather than country-by-country.

## Seller-of-record responsibilities

**Decision (ratified as proposed)**: the seller is the merchant of
record for the underlying service; the platform is a payment facilitator
and marketplace operator, not the merchant of record for tax/consumer-
protection purposes. This should be stated explicitly in the Terms of
Service (not yet drafted; legal must own that document, not this
codebase).

## Stripe Connect account type (Express vs Custom)

**Decision (ratified as proposed)**: Express accounts — already the
implemented default (`providers/stripe.go`). Chosen because it pushes
KYC/compliance UI and most compliance liability onto Stripe. Revisit
Custom only if the product later needs more control over the seller
onboarding UI than Express allows.

## Stablecoin network (Base vs Polygon)

**Decision (ratified as proposed)**: both, config-selected — already the
implemented default (`providers/evm.go` registers `evm-base`/`evm-polygon`
independently based on which is configured).

## Settlement asset and indexer/provider

**Decision**: **USDT is the primary settlement stablecoin** (revising the
earlier USDC-primary proposal to match the ratified USD/USDT base-currency
decision above), with USDC remaining available. No third-party indexer —
`eth_getLogs`/`eth_blockNumber` read directly against the configured
`EVM_*_RPC_URL`, per PLAN.md's stdlib-only constraint. This is already how
`providers/evm.go` is built; no code change is implied by this
ratification, only which asset the admin/finance side should treat as
primary when monitoring volume.

## Crypto custody model and emergency-pause plan

**Decision (ratified as proposed)**: no platform custody, manual treasury
execution — already the implemented model. A single configured treasury
address per chain receives funds directly; there is no platform-held
private key for seller payouts. Payouts reach `ready_for_manual_execution`
and an admin executes the transfer manually from treasury, recording the
tx hash. Emergency pause exists (`platform_settings.payouts_paused`,
`/admin/payouts/pause`). See `docs/runbooks/treasury-custody.md` for the
multi-sig recommendation before real treasury funds are handled at scale.

## Refund policy

**Decision (ratified as proposed)**: full refund available at admin
discretion up to 14 days after `paid_at`, or any time before seller
delivery, whichever is later. Partial refunds remain out of scope for
this pass — flagged as a follow-up if disputes commonly need to settle
for partial amounts. See `docs/runbooks/refund.md`.

## Escrow-like hold and auto-accept policy

**Decision (ratified as proposed)**: a 3-day auto-accept window — already
the seeded `config.AutoAcceptPeriod` default — as the standard hold
period before funds move from `seller_pending` to `seller_available`.

## Dispute policy

**Decision (ratified as proposed)**: disputes are reviewed within 2
business days of opening; resolution options are refund-buyer,
release-to-seller, or partial (manual ledger adjustment via
`/admin/ledger/adjust`).

## Payout policy

**Decision (ratified as proposed)**: the $2,000 manual-review threshold
(`services.IsHighValue`) and the configured wallet-change cooldown
(`WALLET_CHANGE_COOLDOWN`) stand as launch defaults, to be revisited once
real transaction-volume data exists.

## Threat model and data classification

Produced as separate documents: `docs/threat-model.md` and
`docs/data-classification.md`.

## Consent, retention, and privacy policy draft (ops)

Drafted at `docs/privacy-policy-draft.md`. This is a starting point for
legal review, not a publishable policy — see the Deferred Externally
Blocked Items ledger (`docs/deferred-external-items.md`) for what legal
sign-off actually requires before publication.
