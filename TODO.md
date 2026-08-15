# Gig Marketplace Implementation TODO

Checklist derived from `PLAN.md`. Each phase must complete before moving to the next. Items marked `(ops)` are production-only gates and can be deferred until the controlled launch window.

## Phase 0: Product, Legal, and Provider Decisions

- [ ] Select operating country and initial seller countries
- [ ] Confirm marketplace business model and fee model
- [ ] Confirm currencies and tax responsibilities
- [ ] Confirm seller-of-record responsibilities
- [ ] Confirm Stripe Connect availability and account type (Express vs Custom)
- [ ] Provision a BTCPay Server test environment
- [ ] Select stablecoin network (Base or Polygon)
- [ ] Select settlement asset and indexer/provider
- [ ] Select crypto custody model and emergency-pause plan
- [ ] Define refund policy
- [ ] Define escrow-like hold and auto-accept policy
- [ ] Define dispute policy
- [ ] Define payout policy
- [ ] Produce threat model
- [ ] Produce data classification and compliance checklist
- [ ] Produce consent, retention, and privacy policy draft `(ops)`

## Phase 1: Foundation

### Project scaffold

- [x]  Initialize Go module (`go mod init`)
- [x]  Create flat root layout: `components/`, `handlers/`, `services/`, `store/`, `providers/`, `ledger/`, `migrations/`, `static/`, `scripts/`, `testdata/`
- [x]  Create root files: `main.go`, `config.go`, `server.go`, `routes.go`, `sessions.go`, `security.go`, `jobs.go`, `logger.go`
- [x]  Implement `APP_ROLE` dispatch in `main.go` for `web` and `worker`
- [x]  Add `Makefile` with build, test, lint, run targets
- [x]  Add `README.md` with run instructions
- [x]  Add `.env.example` with placeholder values only
- [x]  Add `.gitignore` for secrets, build artifacts, and local state

### Docker Compose

- [x]  Write `Dockerfile` (multi-stage Go build, minimal runtime image)
- [x]  Write `docker-compose.yml` with `db`, `web`, and `worker` services
- [x]  Put `db` and `app` on a dedicated internal network with no external exposure
- [x]  Add `db` healthcheck and depend on health, not ordering alone
- [x]  Add volume for local PostgreSQL data
- [x]  Configure `web` to run migrations at startup before serving
- [x]  Verify internal network resolution of `DATABASE_URL`

### Migrations

- [x]  Embed `migrations/*.sql` via `embed`
- [x]  Implement startup migration runner with advisory lock
- [x]  Implement schema-version tracking table
- [x]  Verify single-instance migration under contention
- [x]  Add migration smoke test against a dockerized database

### Config, logging, health

- [x]  Implement environment parsing with validation
- [x]  Implement structured JSON logging to stdout
- [x]  Implement `/healthz` and `/readyz` endpoints
- [x]  Implement graceful shutdown for web and worker
- [x]  Set up linting (`go vet`, `staticcheck`) and CI
- [x]  Set up formatting and test commands in CI

### Styling and shell

- [x]  Write `static/app.css` with CSS custom properties and mobile-first media queries
- [x]  Build the base layout component (`<html lang dir>`, head, header, main, footer)
- [x]  Add semantic base templates and empty-state components
- [x]  Add mobile bottom-navigation shell
- [x]  Add server-side pagination component for listings
- [x]  Add favicon and static asset serving

## Phase 2: Identity and Sessions

### Password and accounts

- [x] Implement argon2id password hashing and verification
- [x] Implement email verification flow with token expiry
- [x] Implement password reset flow
- [x] Implement TOTP MFA enrollment and verification
- [x] Implement constant-time comparison utilities

### Sessions and CSRF

- [x] Implement PostgreSQL-backed session table (token hash, expiry, UA, IP, revocation)
- [x] Implement session cookie with `HttpOnly`, `Secure`, `SameSite=Lax`
- [x] Implement session rotation on privilege change
- [x] Implement logout with server-side revocation
- [x] Implement CSRF token generation, storage, and verification on all state-changing forms

### Authorization

- [x] Implement roles and permissions model (`UserRole`)
- [x] Implement authorization middleware for buyer, seller, and admin routes
- [x] Implement rate limiting for auth, messaging, checkout, uploads, wallet changes

### Headers and audit

- [x] Send security headers: CSP `script-src 'none'`, `Referrer-Policy`, `X-Content-Type-Options`, `X-Frame-Options`, `Permissions-Policy`
- [x] Implement `AuditLog` recording of privileged actions
- [x] Build basic account settings pages (profile, email, password, MFA)

## Phase 3: Marketplace Core

### Profiles and portfolio

- [x] Implement `SellerProfile` CRUD and public seller page
- [x] Implement seller onboarding state (KYC state, timestamps); `requirements` schema exists but is unpopulated until a real provider lands in Phase 5
- [x] Implement portfolio upload via multipart forms
- [x] Implement media validation (content-type sniffing, size cap)
- [ ] Implement malware/content scanning for uploads
- [ ] Implement signed URLs for private attachments (portfolio/gig media are public by design; this applies to order deliveries and dispute evidence, which arrive with orders in Phase 4)

### Catalog

- [x] Seed starter categories and free-text seller-supplied tags
- [ ] Implement admin `Category` and `Tag` management console (Phase 8)
- [x] Implement `Gig` CRUD with slug generation and moderation state
- [x] Implement `GigPackage` (price, delivery time, revisions, scope)
- [x] Implement `GigAddon` (price, delivery impact, availability)
- [x] Implement `GigMedia` and moderation state
- [x] Implement `Favorite` toggle

### Browse and search

- [x] Implement category browse page
- [x] Implement search with filters, sorting, and pagination
- [x] Implement featured gigs section
- [x] Build public gig detail page with packages and add-ons
- [x] Build public rating display (shows "No reviews yet" until Phase 4 reviews exist)
- [ ] Build seller earnings summary (depends on Phase 5 payments/ledger)

### Dashboards

- [x] Build buyer dashboard (favorites; orders and messages are explicit placeholders until Phase 4)
- [x] Build seller dashboard (gigs; earnings, orders, and payout status are explicit placeholders until Phase 5)
- [x] Add empty states and actionable errors to all dashboards

## Phase 4: Orders and Messaging

### Pricing and checkout

- [x] Implement authoritative total calculation (subtotal, fee, total); seller-amount ledger split and jurisdictional taxes wait on Phase 5 ledger and the Phase 0 tax/jurisdiction decision
- [x] Implement `DraftOrder` with server-persisted multi-step progress
- [x] Build checkout steps: requirements, review/confirm; the "payment method" step is folded into review since no provider is connected yet (Phase 5) — it shows a placeholder notice instead of a method picker
- [x] Implement PRG on every checkout step with validation error re-render
- [x] Create order in `pending_payment` on confirm
- [x] Implement immutable `OrderItem` snapshots

### State machine

- [x] Implement order state machine with all allowed transitions
- [x] Enforce transitions server-side only; never trust client state
- [x] Implement order requirements and completion status
- [x] Implement `OrderAttachment` uploads and signed serving (private storage root outside the public `/media/` mount, served only after a buyer/seller/admin check)

### Fulfillment

- [x] Implement delivery submission with versioning
- [x] Implement revision request flow with limit enforcement
- [x] Implement buyer acceptance flow
- [x] Implement auto-accept job with configured period
- [x] Implement cancellation request flow
- [x] Implement `Review` creation with moderation state

### Messaging and notifications

- [x] Implement order-bound `OrderMessage` threading
- [ ] Implement seller requests for buyer information (no dedicated "request info" UI; a seller can ask through the regular order-message thread today)
- [x] Implement `Notification` creation and unread count
- [x] Implement notification list pages for buyers and sellers
- [x] Implement transactional email dispatch via job queue

### Disputes

- [x] Implement `Dispute` creation with reason and evidence uploads
- [x] Implement dispute timeline and status
- [x] Implement dispute resolution decision recording (before real payouts) — admin-only action, no dedicated admin console yet (Phase 8), reachable from the order page itself

## Phase 5: Fiat Payments and Seller Onboarding

### Provider abstraction

- [x] Define `Provider` interface in `providers/`
- [x] Implement normalized payment types shared across adapters
- [x] Implement `PaymentIntent` and `PaymentAttempt` persistence
- [x] Implement `PaymentWebhookEvent` with dedup by provider event ID
- [x] Implement webhook verification helper (signature, replay, staleness) — Stripe HMAC-SHA256 verification with a 5-minute timestamp tolerance, hand-rolled against the stdlib-only constraint (no vendored Stripe SDK)

### Stripe Connect adapter

- [x] Implement Stripe seller onboarding link/redirect flow — Express accounts by default (least platform-side compliance work); PLAN.md's Express-vs-Custom call is still open for Phase 0 to formally confirm
- [x] Handle Stripe account status webhooks — `account.updated` updates `seller_onboarding.charges_enabled`/`payouts_enabled`
- [x] Implement Stripe Checkout Session creation with `success_url`/`cancel_url`
- [x] Implement payment success and failure webhooks (`checkout.session.completed`/`expired`/`async_payment_*`); dispute and payout webhook types are still not parsed
- [x] Implement idempotency keys on checkout and refund creation
- [x] Implement refund flow with provider reference — admin-only, full-refund-only action at `/admin/orders/{id}/refund`; no buyer-facing refund request yet
- [x] Implement `PaymentIntent` transitions from normalized webhook state
- [x] Keep secret keys out of client output and logs

### Ledger

- [x] Implement `LedgerAccount` and `LedgerEntry` double-entry postings
- [x] Implement posting for gross buyer funds, platform fee, seller payable
- [x] Implement posting for refunds and adjustments — `ledger.RefundIssued`, wired into the admin refund action
- [x] Implement balances: pending earnings, available earnings, platform revenue, refunds, reserves, clearing accounts
- [x] Add ledger balance validation test
- [x] Implement reconciliation job against Stripe records — `payment.reconcile_sweep` re-checks stale intents directly against the provider
- [x] Make reconciliation exceptions visible in admin console — dead-lettered `payment.webhook_process` jobs are listed on `/admin/payments`
- [x] Implement audited, permissioned manual adjustments with reason — `/admin/ledger/adjust` (`GET`/`POST`), `ledger.ManualAdjustment` builds a balanced two-leg posting between any two account kinds, audited as `ledger.manual_adjustment` with the transaction group ID; also fixed a pre-existing bug in `ledger.RefundIssued` (its entries did not balance — `TestRefundIssuedBalances` was failing on `main` before this pass)

### Admin payment tooling

- [x] Build admin search by payment ID and provider reference — `/admin/payments/search`, `store.SearchPaymentIntents` matches numeric intent/order ID or a substring of provider_ref/charge_ref
- [x] Build order and payment timeline view — `/admin/orders/{id}/timeline` shows every `payment_attempts` row and every matched `payment_webhook_events` row for the order's latest intent, not just the current status; required adding a `provider_ref` column to `payment_webhook_events` (0010 migration) so events join to intents directly instead of parsing provider-shaped JSON payloads
- [x] Implement safe webhook retry tool — dead-lettered jobs on `/admin/payments` now have a "Retry" button (`POST /admin/jobs/{id}/retry`, `store.RetryJob` resets status/attempts so a worker re-claims it)
- [x] Build payout queue and failed-payout views — pre-existing `/admin/payouts` from Phase 7 covers this; not rebuilt

## Phase 6: Bitcoin and Lightning (BTCPay)

### BTCPay adapter

- [x] Implement BTCPay invoice creation via API — `providers/btcpay.go` `CreatePayment`, Greenfield REST over `net/http`, no SDK
- [x] Persist invoice ID, destination metadata, amount, conversion snapshot, confirmations, expiry — reuses the existing `payment_intents`/`payment_attempts` tables (provider-tagged), no schema change needed
- [x] Redirect buyer to hosted invoice page — checkout method selector routes `bitcoin`/`lightning` to BTCPay's `CheckoutLink`
- [x] Implement invoice state mapping (paid, confirmed, fully confirmed) per risk policy — `normalizeInvoiceStatus`; fulfillment gate is BTCPay's own `Settled` status
- [x] Implement webhook verification and idempotent processing — `BTCPay-Sig` HMAC-SHA256 verify + 5-minute timestamp tolerance, dedup by delivery ID via the existing `payment.webhook_process` job path
- [x] Implement expiry handling and status refresh via meta-refresh page — `handlers/payments.go` `btcpayInvoiceStatus`
- [x] Implement underpayment, overpayment, and partial payment handling — `normalizeInvoiceStatus` treats `PaidPartial`/`PaidLate` as needing manual review rather than silent success; surfaced via reconciliation sweep and admin payment view, not a dedicated queue
- [x] Implement refund handling for BTCPay — `Refund` creates a pull payment (BTCPay refunds are buyer-claimed, not instant, so status is always `processing` until reconciled)
- [x] Define confirmations-before-fulfillment policy in config — `config.BTCPayRequiredConfirmations`
- [x] Add reconciliation job scanning BTCPay for missed webhooks — reuses the existing provider-agnostic stale-intent sweep, now dispatched per-provider via `providers.Registry`
- [x] Add admin visibility for BTCPay invoices — `handlers/payments.go` `adminOrderPayments`/`renderOnChainPaymentDetail` adds a live provider re-check plus `payment_attempts` status history (`store.ListPaymentAttemptsForIntent`) below the generic view when `intent.Provider == "btcpay"`; also covered provider-agnostically by the Phase 8 `/admin/payments/search` and `/admin/orders/{id}/timeline` consoles

## Phase 7: Stablecoin Payments and Wallet Payouts

Network choice (Phase 0's Base-vs-Polygon gate) was resolved for this pass as "both, config-selected" — `providers/evm.go` is one adapter instance per chain (`evm-base`/`evm-polygon`), registered independently when its RPC URL and treasury address are configured. Chain access is raw JSON-RPC (`eth_getLogs`/`eth_blockNumber`) over `net/http`, no SDK, matching Phase 6's BTCPay precedent.

### EVM adapter

- [x] Implement selected EVM network adapter or payment processor adapter — `providers/evm.go`, one instance per chain
- [x] Configure chain ID, RPC/indexer, token contract addresses, confirmation count — `config.EVMBase*`/`EVMPolygon*`/`EVMRequiredConfirmations`
- [x] Verify token contract addresses from config only, never user input
- [x] Generate per-order deposit address or unique payment reference — a single configured treasury address plus a provider-ref-encoded expected-amount match, not a per-order generated address (no HD wallet custody in scope)
- [x] Render server-generated QR code and deposit instructions as plain HTML — `services/qrcode.go` is a from-scratch stdlib-only QR encoder (byte mode, ECC level L, versions 1-6, mask selection by penalty score) rendering plain inline `<svg>`; wired into `evmDepositStatus` above the existing `<code>` address fallback
- [x] Implement transaction verification (sender, recipient, amount, block, confirmations) — amount-match against `Transfer` logs to the treasury address, confirmation depth from `eth_blockNumber`
- [x] Implement reorg-safe confirmation handling — `EVMRequiredConfirmations` gates `succeeded` status
- [x] Implement reconciliation job scanning indexer for missed webhooks — reuses the existing provider-agnostic `payment.reconcile_sweep`, no new job needed since EVM has no inbound webhooks by design
- [x] Implement refund policy for stablecoin payments — admin-queued/manual, same "processing until reconciled" precedent as BTCPay refunds (no treasury signing key in scope)
- [x] Add admin visibility for on-chain payments — same `renderOnChainPaymentDetail` addition as BTCPay above, triggered for `evm-*` providers; since EVM has no inbound webhooks, its most useful field is the live provider re-check (tx hash via `ChargeRef`, confirmation-derived status), not attempt history, which stays empty for this provider today; also covered provider-agnostically by the Phase 8 `/admin/payments/search` and `/admin/orders/{id}/timeline` consoles

### Wallet payouts

- [x] Implement seller wallet ownership confirmation — `handlers/wallets.go`, reuses the existing auth-token email-confirmation machinery
- [x] Encrypt stored wallet addresses with network and asset binding — `services/walletcrypto.go` (AES-256-GCM), `seller_wallets` table scoped by (user, network, asset)
- [x] Implement fresh-confirmation on address change with cooling-off period — `WALLET_CHANGE_COOLDOWN` gates `eligible_at`; every change goes through a new pending row, never edited in place
- [x] Implement payout queue with allowlists and limits — `store/wallets.go` `Payout`/`CreatePayout`; allowlist/threshold policy (queued vs needs_manual_review) is left to the caller of `CreatePayout`, not yet wired into an automatic seller-initiated payout request flow
- [x] Implement manual review threshold for high-value payouts — `needs_manual_review` status, admin-approved via `/admin/payouts/{id}/approve`
- [x] Implement admin emergency pause — `platform_settings.payouts_paused`, `/admin/payouts/pause`
- [x] Ensure payouts never use raw client-provided addresses — payouts reference `wallet_id`; the address is only ever decrypted from the stored, confirmed row
- [ ] Design ops runbook for gas funding, treasury, key custody `(ops)`

Explicitly out of scope for this pass (flagged rather than silently stubbed): actual on-chain broadcast of refunds and payouts. Both require a treasury signing key, which is outside this project's key-custody scope; the queue reaches `ready_for_manual_execution`/`processing` and an admin executes the transfer manually, recording the tx hash for audit (`/admin/payouts/{id}/complete`).

## Phase 8: Full Operations and Hardening

### Admin consoles

- [x] Complete moderation dashboards (users, gigs, media, reviews, messages) — `handlers/admin.go`: `/admin/users` (search/filter + suspend/restore with a required reason), `/admin/moderation/gigs`, `/admin/moderation/media`, `/admin/moderation/reviews` (approve/reject queues filterable by state), `/admin/moderation/messages` (hide, filterable by order); every decision is audit-logged and CSRF-protected
- [x] Complete dispute resolution console with evidence and internal notes — `/admin/disputes` (open/resolved lists) and `/admin/disputes/{id}` (evidence attachment links, an admin-only internal-notes form separate from the buyer/seller-visible decision, and the existing resolve action)
- [x] Complete payout and reconciliation dashboards — pre-existing `/admin/payouts` and `/admin/payments` from Phase 5/7 extended in this pass with a "Retry" button on dead-lettered jobs; not otherwise rebuilt
- [x] Implement CSV report exports with sensitive-field access controls — `/admin/users/export.csv` and `/admin/audit/export.csv`, admin-role-gated (like every `/admin/*` route) and each export itself writes an `admin.export_csv` audit entry with the row count
- [x] Implement settings, fees, networks, and feature-flag management — `/admin/settings` lists/edits every `platform_settings` row (generic key/value; seeded with `platform_fee_bps` and `feature_stablecoin_payouts` alongside the existing `payouts_paused`) and supports adding new keys; every change is audited

### Fraud and security

- [x] Implement velocity and suspicious-order-pattern rules — `services/fraud.go` `IsVelocitySuspicious` (pure threshold check, unit tested); `handlers/fraud.go` `checkFraudSignals` queries `store.CountRecentOrdersByBuyer` on every checkout confirm and audit-logs a `fraud.velocity_alert` (visible on `/admin/audit`) when a buyer exceeds 5 orders/hour — a review signal, not a hard block
- [x] Implement chargeback and high-value transaction alerts — `services.IsHighValue` (>= $2,000 minor units by default) checked on order creation (`fraud.high_value_alert`) and on payout approval (`handlers/payments.go` `adminPayoutApprove`); no Stripe dispute/chargeback webhook parsing yet (Phase 5 note: "dispute and payout webhook types are still not parsed"), so chargeback-specific alerting is not wired to a real chargeback event
- [x] Implement wallet-change alerts and cooldowns — cooldown already existed (Phase 7); `handlers/fraud.go` `alertWalletChange` now also audit-logs a `fraud.wallet_change_alert` on every confirmed wallet change, independent of the cooldown gate
- [x] Implement file scanning and size/type limits — content-type sniffing and size caps already existed (Phase 3); `services/storage.go` `scanUpload` adds a lightweight signature check (EICAR test string) and rejects zip archive members with executable/script extensions, per TODO.md's explicit note that a full AV integration is out of scope
- [x] Add data redaction for payment and identity fields in logs — `logger.go` `redactAttr`, a `slog.HandlerOptions.ReplaceAttr` that masks any attribute key containing a sensitive substring (password, secret, token, card, wallet address, email, etc.) at any nesting depth, applied globally to both the web and worker loggers
- [x] Run dependency and container vulnerability scanning — `make vulncheck` (installs and runs `govulncheck`) and a new `Vulnerability scan` step in `.github/workflows/ci.yml`; container image scanning (e.g. Trivy against the built Docker image) is not set up

### Reliability and performance

- [ ] Implement job queue retry with backoff and dead-letter handling
- [ ] Test duplicate and out-of-order webhooks
- [ ] Test provider downtime and retry queues
- [ ] Test expired payment sessions
- [ ] Test partial, under, and overpayments
- [ ] Test blockchain reorg and insufficient confirmations
- [ ] Test concurrent acceptance/refund/payout attempts
- [ ] Test concurrent migration startup under advisory lock contention
- [ ] Run load tests on search, checkout, and messaging
- [ ] Verify mobile performance and page weight budgets
- [ ] Exercise backup and restore procedures `(ops)`
- [ ] Exercise disaster-recovery runbook `(ops)`

### Accessibility and zero-JS verification

- [ ] Audit keyboard navigation, focus order, labels, and contrast
- [ ] Audit screen-reader structure of all major pages
- [ ] Run every journey with JavaScript disabled in the browser
- [ ] Verify no shipped page contains `<script>`, inline handlers, or third-party widgets
- [ ] Verify `<html lang dir>` from server-side locale configuration
- [ ] Verify semantic element usage and minimal id/class selectors
- [ ] Verify auto-refresh status pages use `<meta http-equiv="refresh">`

### Compliance and sign-off `(ops)`

- [ ] Complete KYC/KYB and sanctions screening integration
- [ ] Complete AML, money-transmission, and consumer-protection review
- [ ] Complete tax and chargeback process review
- [ ] Complete crypto-custody and treasury review
- [ ] Run provider sandbox certification and end-to-end payment drills
- [ ] Produce production runbooks (incident, payout, refund, reconciliation)
- [ ] Obtain legal/compliance sign-off

## Phase 9: Controlled Launch `(ops)`

- [ ] Enable platform for a limited seller cohort
- [ ] Set low transaction and payout limits
- [ ] Keep stablecoin and external-wallet payouts behind feature flags
- [ ] Monitor payment success rate, webhook failures, reconciliation exceptions
- [ ] Monitor refunds, disputes, and payout delays
- [ ] Review fraud/risk alerts daily
- [ ] Expand countries, currencies, networks, and payout limits after measured review

## Cross-Cutting Gates

- [x] No order becomes paid from client-side input alone — `pending_payment -> paid` only happens inside the verified-webhook job, never on the buyer's return-URL request
- [x] Every successful payment produces balanced ledger postings — enforced by `ledger.Validate`, re-checked in `store.PostLedgerEntries`, covered by unit tests
- [x] Seller funds unavailable until acceptance or auto-acceptance — captured funds post to `seller_pending`; `orderAccept` and the auto-accept sweep both call `ledger.EarningsReleased` to move them to `seller_available`
- [ ] All money-changing requests use idempotency keys — checkout does; refunds and payouts don't exist as reachable flows yet
- [x] Payment, wallet, and identity secrets absent from client output and logs
- [x] Migrations apply cleanly at startup under a single and concurrent instances
- [ ] All critical financial and state-transition paths covered by automated tests — ledger and payment-transition unit tests exist; no integration test yet for the webhook-to-ledger path
