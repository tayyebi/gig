# Payout Runbook

Status: **DRAFT**, written from the current implementation
(`store/wallets.go`, `handlers/payments.go`, `handlers/wallets.go`).

## How a payout reaches an admin

1. A seller requests a payout at `/sell/payouts` against a confirmed,
   cooling-off-cleared wallet, capped at their `seller_available` ledger
   balance minus their own in-flight payouts.
2. `services.IsHighValue` (currently ≥ $2,000) routes the request straight
   to `needs_manual_review`; everything else starts `queued`.
3. Stripe/fiat sellers do not go through this queue at all — Stripe
   Connect pays them out automatically once `payouts_enabled` is true on
   their account (`seller_onboarding.PayoutsEnabled`).

## Approving a wallet payout

1. Go to `/admin/payouts`. Review `needs_manual_review` and `queued`
   entries.
2. Confirm the payout is legitimate: check the seller's order history and
   `/admin/audit` for recent `fraud.*` entries on that seller or their
   wallet.
3. Click **Approve** (`adminPayoutApprove`) — this transitions
   `needs_manual_review` → `ready_for_manual_execution` and fires a
   `fraud.high_value_alert` audit entry if the amount is high-value (a
   double-check even if it was already routed here for that reason).
4. `queued` payouts do not currently have a UI step to move them to
   `ready_for_manual_execution` automatically — **this is a gap**: today
   only manual-review payouts have an explicit approve action. A `queued`
   payout should get a scheduled sweep or a manual "release" action before
   this queue is relied upon at any real volume; flagged here rather than
   silently assumed complete.

## Executing a payout

There is no automated on-chain broadcast (explicitly out of scope — no
treasury signing key exists in this codebase's custody model, see
`docs/runbooks/treasury-custody.md`).

1. Once a payout is `ready_for_manual_execution`, an operator with access
   to the treasury wallet manually sends the funds from the treasury
   address to the seller's decrypted wallet address.
2. **Never** get the destination address from anywhere except the admin
   console's display of the payout, which is sourced from the encrypted,
   confirmed `seller_wallets` row via `wallet_id` — never from a support
   ticket, email, or chat message, even if it claims to be from the
   seller. This is the primary social-engineering risk in this flow.
3. Record the transaction hash via **Complete**
   (`adminPayoutComplete` → `TransitionPayout(..., PayoutCompleted, ...,
   txHash)`). This is the audit record that the transfer happened.

## If a payout looks wrong after the fact

1. Do not attempt to reverse an on-chain transfer — it's not possible.
2. If the amount was wrong, record a `ManualAdjustment` ledger entry via
   `/admin/ledger/adjust` to correct the seller's balance, with a clear
   reason referencing the payout ID.
3. Treat it as a security incident if the destination wallet doesn't match
   the one on file (see `docs/runbooks/incident.md`).

## Emergency pause

`/admin/payouts/pause` flips `platform_settings.payouts_paused`
platform-wide. New payout requests are still accepted while paused (a
seller can still queue one), but treat the pause as "do not execute any
`ready_for_manual_execution` payout" until it's lifted.
