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
- [ ] Implement TOTP MFA enrollment and verification
- [x] Implement constant-time comparison utilities

### Sessions and CSRF

- [x] Implement PostgreSQL-backed session table (token hash, expiry, UA, IP, revocation)
- [x] Implement session cookie with `HttpOnly`, `Secure`, `SameSite=Lax`
- [x] Implement session rotation on privilege change
- [x] Implement logout with server-side revocation
- [x] Implement CSRF token generation, storage, and verification on all state-changing forms

### Authorization

- [x] Implement roles and permissions model (`UserRole`)
- [ ] Implement authorization middleware for buyer, seller, and admin routes
- [x] Implement rate limiting for auth, messaging, checkout, uploads, wallet changes

### Headers and audit

- [x] Send security headers: CSP `script-src 'none'`, `Referrer-Policy`, `X-Content-Type-Options`, `X-Frame-Options`, `Permissions-Policy`
- [x] Implement `AuditLog` recording of privileged actions
- [ ] Build basic account settings pages (profile, email, password, MFA)

## Phase 3: Marketplace Core

### Profiles and portfolio

- [ ] Implement `SellerProfile` CRUD and public seller page
- [ ] Implement seller onboarding state (KYC state, requirements, timestamps)
- [ ] Implement portfolio upload via multipart forms
- [ ] Implement media validation (type, size, content) and scanning
- [ ] Implement signed URLs for private attachments

### Catalog

- [ ] Implement `Category` and `Tag` management
- [ ] Implement `Gig` CRUD with slug generation and moderation state
- [ ] Implement `GigPackage` (price, delivery time, revisions, scope)
- [ ] Implement `GigAddon` (price, delivery impact, availability)
- [ ] Implement `GigMedia` and moderation state
- [ ] Implement `Favorite` toggle

### Browse and search

- [ ] Implement category browse page
- [ ] Implement search with filters, sorting, and pagination
- [ ] Implement featured gigs section
- [ ] Build public gig detail page with packages and add-ons
- [ ] Build seller earnings summary and public rating display

### Dashboards

- [ ] Build buyer dashboard (orders, favorites, messages preview)
- [ ] Build seller dashboard (gigs, earnings, orders, payout status)
- [ ] Add empty states and actionable errors to all dashboards

## Phase 4: Orders and Messaging

### Pricing and checkout

- [ ] Implement authoritative total calculation (subtotal, fee, seller amount, taxes, total)
- [ ] Implement `DraftOrder` with server-persisted multi-step progress
- [ ] Build checkout steps: requirements, payment method, review, confirm
- [ ] Implement PRG on every checkout step with validation error re-render
- [ ] Create order in `pending_payment` on confirm
- [ ] Implement immutable `OrderItem` snapshots

### State machine

- [ ] Implement order state machine with all allowed transitions
- [ ] Enforce transitions server-side only; never trust client state
- [ ] Implement order requirements and completion status
- [ ] Implement `OrderAttachment` uploads and signed serving

### Fulfillment

- [ ] Implement delivery submission with versioning
- [ ] Implement revision request flow with limit enforcement
- [ ] Implement buyer acceptance flow
- [ ] Implement auto-accept job with configured period
- [ ] Implement cancellation request flow
- [ ] Implement `Review` creation with moderation state

### Messaging and notifications

- [ ] Implement order-bound `OrderMessage` threading
- [ ] Implement seller requests for buyer information
- [ ] Implement `Notification` creation and unread count
- [ ] Implement notification list pages for buyers and sellers
- [ ] Implement transactional email dispatch via job queue

### Disputes

- [ ] Implement `Dispute` creation with reason and evidence uploads
- [ ] Implement dispute timeline and status
- [ ] Implement dispute resolution decision recording (before real payouts)

## Phase 5: Fiat Payments and Seller Onboarding

### Provider abstraction

- [ ] Define `Provider` interface in `providers/`
- [ ] Implement normalized payment types shared across adapters
- [ ] Implement `PaymentIntent` and `PaymentAttempt` persistence
- [ ] Implement `PaymentWebhookEvent` with dedup by provider event ID
- [ ] Implement webhook verification helper (signature, replay, staleness)

### Stripe Connect adapter

- [ ] Implement Stripe seller onboarding link/redirect flow
- [ ] Handle Stripe account status webhooks
- [ ] Implement Stripe Checkout Session creation with `success_url`/`cancel_url`
- [ ] Implement payment success, failure, refund, dispute, and payout webhooks
- [ ] Implement idempotency keys on checkout and refund creation
- [ ] Implement refund flow with provider reference
- [ ] Implement `PaymentIntent` transitions from normalized webhook state
- [ ] Keep secret keys out of client output and logs

### Ledger

- [ ] Implement `LedgerAccount` and `LedgerEntry` double-entry postings
- [ ] Implement posting for gross buyer funds, platform fee, seller payable
- [ ] Implement posting for refunds and adjustments
- [ ] Implement balances: pending earnings, available earnings, platform revenue, refunds, reserves, clearing accounts
- [ ] Add ledger balance validation test
- [ ] Implement reconciliation job against Stripe records
- [ ] Make reconciliation exceptions visible in admin console
- [ ] Implement audited, permissioned manual adjustments with reason

### Admin payment tooling

- [ ] Build admin search by payment ID and provider reference
- [ ] Build order and payment timeline view
- [ ] Implement safe webhook retry tool
- [ ] Build payout queue and failed-payout views

## Phase 6: Bitcoin and Lightning (BTCPay)

### BTCPay adapter

- [ ] Implement BTCPay invoice creation via API
- [ ] Persist invoice ID, destination metadata, amount, conversion snapshot, confirmations, expiry
- [ ] Redirect buyer to hosted invoice page
- [ ] Implement invoice state mapping (paid, confirmed, fully confirmed) per risk policy
- [ ] Implement webhook verification and idempotent processing
- [ ] Implement expiry handling and status refresh via meta-refresh page
- [ ] Implement underpayment, overpayment, and partial payment handling
- [ ] Implement refund handling for BTCPay
- [ ] Define confirmations-before-fulfillment policy in config
- [ ] Add reconciliation job scanning BTCPay for missed webhooks
- [ ] Add admin visibility for BTCPay invoices

## Phase 7: Stablecoin Payments and Wallet Payouts

### EVM adapter

- [ ] Implement selected EVM network adapter or payment processor adapter
- [ ] Configure chain ID, RPC/indexer, token contract addresses, confirmation count
- [ ] Verify token contract addresses from config only, never user input
- [ ] Generate per-order deposit address or unique payment reference
- [ ] Render server-generated QR code and deposit instructions as plain HTML
- [ ] Implement transaction verification (sender, recipient, amount, block, confirmations)
- [ ] Implement reorg-safe confirmation handling
- [ ] Implement reconciliation job scanning indexer for missed webhooks
- [ ] Implement refund policy for stablecoin payments
- [ ] Add admin visibility for on-chain payments

### Wallet payouts

- [ ] Implement seller wallet ownership confirmation
- [ ] Encrypt stored wallet addresses with network and asset binding
- [ ] Implement fresh-confirmation on address change with cooling-off period
- [ ] Implement payout queue with allowlists and limits
- [ ] Implement manual review threshold for high-value payouts
- [ ] Implement admin emergency pause
- [ ] Ensure payouts never use raw client-provided addresses
- [ ] Design ops runbook for gas funding, treasury, key custody `(ops)`

## Phase 8: Full Operations and Hardening

### Admin consoles

- [ ] Complete moderation dashboards (users, gigs, media, reviews, messages)
- [ ] Complete dispute resolution console with evidence and internal notes
- [ ] Complete payout and reconciliation dashboards
- [ ] Implement CSV report exports with sensitive-field access controls
- [ ] Implement settings, fees, networks, and feature-flag management

### Fraud and security

- [ ] Implement velocity and suspicious-order-pattern rules
- [ ] Implement chargeback and high-value transaction alerts
- [ ] Implement wallet-change alerts and cooldowns
- [ ] Implement file scanning and size/type limits
- [ ] Add data redaction for payment and identity fields in logs
- [ ] Run dependency and container vulnerability scanning

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

- [ ] No order becomes paid from client-side input alone
- [ ] Every successful payment produces balanced ledger postings
- [ ] Seller funds unavailable until acceptance or auto-acceptance
- [ ] All money-changing requests use idempotency keys
- [ ] Payment, wallet, and identity secrets absent from client output and logs
- [ ] Migrations apply cleanly at startup under a single and concurrent instances
- [ ] All critical financial and state-transition paths covered by automated tests
