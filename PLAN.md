# Gig Marketplace Implementation Plan

## 1. Product Scope

Build a full-platform, mobile-first gig marketplace delivered as a responsive, **zero-JavaScript** web app. Buyers can discover services, purchase fixed-price gigs, communicate with sellers, review completed work, and pay through multiple fiat and crypto rails. Sellers can publish gigs, manage availability, fulfill orders, connect payout accounts, and receive funds after buyer acceptance. Administrators can moderate the marketplace, manage disputes, review payment activity, and operate the platform.

The first release must support:

- Buyer, seller, and administrator roles
- Seller profiles, portfolios, ratings, reviews, and verification status
- Search, categories, tags, sorting, and filtering
- Fixed-price gigs with tiers, add-ons, delivery times, revisions, and requirements
- Multi-step checkout and onboarding journeys built entirely from HTML forms
- Order lifecycle with delivery, buyer acceptance, revision requests, cancellation, and dispute handling
- Buyer and seller messaging tied to an order
- Notifications for important marketplace, order, and payment events
- Platform service fees and seller earnings ledger
- Seller onboarding and payout configuration
- Stripe Connect marketplace payments via hosted checkout
- BTCPay Server for Bitcoin on-chain and Lightning payments
- Stablecoin payments on an EVM-compatible network such as Base or Polygon
- External-wallet payout support where legally and operationally appropriate
- Admin dashboards for users, gigs, orders, disputes, payments, fees, and audit events
- Responsive mobile-first web experience, with desktop layouts supported

The platform must use a provider abstraction so country-specific payment availability, tax treatment, KYC requirements, currencies, and payout restrictions can be configured without changing marketplace domain logic.

### Hard Constraints

- **Zero JavaScript in the front end.** The application ships no `<script>` elements, no inline handlers, no third-party widgets, no hydration, and no service workers. Every page works with JavaScript fully disabled.
- **No UI frameworks.** No Tailwind, Bootstrap, or CSS frameworks. Styling comes from a single authored stylesheet using CSS custom properties and native CSS.
- **Go backend using only the standard library** for HTTP handling, templating, and routing.
- **Everything runs in Docker Compose** on an internal Docker network, with PostgreSQL as the database.
- **Flat repository structure**: root-level Go files plus exactly one level of subdirectories.
- **Database migrations run at application startup** from embedded SQL, with advisory locking.

## 2. Goals and Non-Goals

### Goals

- Create a trustworthy marketplace with clear order states and protected fund release.
- Make payment selection explicit and understandable across fiat, Bitcoin, Lightning, and stablecoins.
- Keep payment confirmation server-authoritative and webhook-driven.
- Maintain a complete financial ledger that can be reconciled independently of provider dashboards.
- Make the core marketplace usable on narrow mobile screens first, without JavaScript.
- Keep the server the single source of truth for all state changes; the browser only submits forms and renders pages.
- Make disputes, refunds, cancellations, and manual intervention operationally manageable.
- Keep provider integrations replaceable through adapters and normalized payment records.
- Keep the codebase small and navigable through a flat, concern-separated layout.
- Ship accessible, semantic HTML that relies on native browser behavior and HTML attributes.

### Non-Goals for the Initial Build

- Native iOS or Android applications
- A PWA with service workers, offline caching, or push notifications (all require JavaScript)
- WebSockets, Server-Sent Events, or any realtime client channel (polling and page refresh only)
- Client-side form validation; all validation happens server-side and re-renders the form with errors
- Permissionless token issuance or platform cryptocurrency
- Anonymous seller payouts without required identity and compliance checks
- Fully automated dispute adjudication
- Supporting every crypto asset or blockchain from launch
- Country-specific legal certification before the operating jurisdiction is selected
- CSS frameworks, component libraries, or JavaScript build tooling of any kind

## 3. Recommended Architecture

### Runtime Stack

- **Language**: Go, using the standard library only for HTTP, routing, templating, and testing
- **Routing**: Go 1.22+ `net/http` `ServeMux` method- and path-pattern routing
- **Templating**: `html/template` fragments composed by Go component functions (component-style rendering)
- **Database**: PostgreSQL accessed through the `database/sql` interface with `pgx`
- **Background work**: PostgreSQL-backed job queue (polling with `FOR UPDATE SKIP LOCKED`) processed by worker goroutines; no Redis required
- **File storage**: local volume mounted into the container, or S3-compatible storage behind an interface
- **Containerization**: Docker Compose with `db`, `web`, and `worker` services on an internal Docker network
- **Logging and monitoring**: structured JSON logs to stdout; Prometheus metrics endpoint; health endpoints
- **Email**: transactional email over SMTP or an SMTP-compatible provider

### Docker Compose Topology

```
gig-network (internal, driver: bridge)
  ├── db        postgres:17     port 5432 published only for local tooling (optional)
  └── app       go build image  services: web  (migrations + HTTP server)
                                services: worker (job queue consumer)
```

- Compose places `db` and `app` on a dedicated internal network with no default external exposure.
- `web` applies embedded migrations at startup, protected by a PostgreSQL advisory lock, before opening for traffic.
- `worker` runs the same binary with a separate role, so there is one image and one source of truth.
- Depend on `db` healthcheck, not `depends_on` ordering alone.

### Domain Boundaries

Keep these modules independent:

- Identity and access
- User profiles and seller onboarding
- Catalog and search
- Orders and fulfillment
- Messaging and notifications
- Payments and payment providers
- Wallets and payout destinations
- Accounting ledger and platform fees
- Reviews and reputation
- Disputes and moderation
- Administration and audit logs

Business rules should operate on normalized internal entities. Provider-specific SDKs and API payloads must stay inside provider adapters.

## 4. Repository Structure

Flat root files plus exactly one level of subdirectories. Each directory is a Go package and a distinct concern. No nesting beyond one level.

```
gig/
├── Dockerfile
├── docker-compose.yml
├── Makefile
├── go.mod
├── go.sum
├── .env.example
├── .gitignore
├── README.md
├── main.go              # entrypoint; dispatches to web or worker by APP_ROLE
├── config.go            # environment parsing and validation
├── server.go            # HTTP server setup, middleware wiring, graceful shutdown
├── routes.go            # ServeMux pattern registration
├── sessions.go          # cookie session creation, lookup, rotation, revocation
├── security.go          # CSRF tokens, headers, password hashing (argon2), constant-time compare
├── jobs.go              # Postgres-backed job queue poller and dispatcher
├── logger.go            # structured logging setup
├── components/          # HTML component functions and template fragments
├── handlers/            # HTTP handlers grouped by concern
├── services/            # domain logic (orders, payouts, disputes, moderation)
├── store/               # PostgreSQL data access and queries
├── providers/           # payment adapters: stripe, btcpay, evm, wallets
├── ledger/              # double-entry accounting postings and reconciliation
├── migrations/          # embedded .sql files, applied at startup
├── static/              # app.css, images, fonts, favicon
├── testdata/            # fixtures for tests
└── scripts/             # local dev helpers
```

Design rules for this layout:

- Handlers are thin: parse input, call a service, render a page or redirect.
- Services hold business rules and are testable without HTTP.
- Store owns SQL and transactions; it never renders HTML.
- Providers map provider concepts to normalized internal types.
- Components never query the database directly; they receive data.
- `main` package files stay small and only wire things together.
- Nothing may reach across concern boundaries except through the intended packages.

## 5. Core Roles and Permissions

### Buyer

- Browse, search, favorite, and purchase gigs
- Select an available payment method
- Provide order requirements through multi-step forms
- Message the seller
- Review delivery and request revisions
- Accept delivery or open a dispute
- Request eligible cancellations or refunds
- View payment receipts, order history, and transaction status

### Seller

- Create and manage a public profile
- Create, edit, pause, and archive gigs
- Configure packages, add-ons, delivery times, and revision limits
- Receive and fulfill orders
- Request required buyer information
- Deliver files and messages through the order workspace
- Configure supported payout destinations
- View earnings, fees, payment status, and payout history
- Respond to disputes and moderation actions

### Administrator

- Manage users, roles, verification states, and account restrictions
- Review, approve, reject, or remove gigs
- Manage categories, tags, featured content, and platform settings
- Inspect orders, payment attempts, webhooks, ledger entries, refunds, and payouts
- Review and resolve disputes with documented decisions
- Trigger or retry operational actions with audited permissions
- Configure payment methods, fee schedules, settlement rules, and supported networks
- Export operational and financial reports

Use least-privilege authorization on every server-side action. Never rely on hidden UI controls as the permission boundary.

## 6. Rendering and Front-End Approach

### Component-Style Rendering

- HTML is generated server-side by Go functions in `components/`.
- Each component is a function that takes data and returns `template.HTML` composed from an `html/template` fragment parsed once at startup.
- Pages are assembled by composing components inside a layout; layouts render `<html>`, `<head>`, `<body>`, and shell elements.
- Because components are Go functions, they can take arguments, reuse partials, and be unit-tested without HTTP.

### Semantic HTML and Minimal Selectors

- Use semantic elements: `header`, `nav`, `main`, `article`, `section`, `aside`, `footer`, `ul`, `ol`, `dl`, `table`, `form`, `fieldset`, `legend`, `label`, `time`, `figure`, `figcaption`.
- Use HTML5 attributes instead of classes wherever possible:

```html
<html lang="en" dir="ltr">
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="icon" href="/static/favicon.ico">
  <input name="q" type="search" autocomplete="off" inputmode="search">
  <input name="email" type="email" autocomplete="email" inputmode="email" required>
  <img src="..." alt="..." width="400" height="300" loading="lazy">
  <time datetime="2026-01-01T00:00:00Z">...</time>
```

- `lang` and `dir` come from server-side locale configuration, applied to the `<html>` element.
- Keep class and id usage to the minimum needed for CSS and accessible labels. Prefer element and attribute selectors, structural selectors, and a small set of reusable class names.
- Every form field has an associated `<label>`. Use `fieldset`/`legend`, `required`, `autocomplete`, `inputmode`, and appropriate `type` attributes so mobile keyboards and native validation behave correctly.
- Use native `<form action method>`, `PRG` (Post/Redirect/Get), and `403`/`422` re-renders for all interactions.
- For status pages that should auto-refresh (payment pending, blockchain confirmations), use `<meta http-equiv="refresh">`, not JavaScript.

### Styling

- A single `static/app.css` using CSS custom properties, logical properties, and mobile-first media queries.
- No CSS framework, no utility framework, no build step.
- Styling strategy uses the small set of semantic class names plus element/attribute selectors.

## 7. Data Model

Use UUIDs or another non-sequential public identifier. Store money as integer minor units with an explicit ISO currency code. Store crypto quantities as precise decimal or integer smallest-unit values with asset and network metadata; never use binary floating point for financial values.

### Identity

- `User`: account identity, role, status, locale, contact information
- `Session`: server-side session record, token hash, expiry, user agent, IP, revocation time
- `UserRole`: role assignments if multiple roles are supported
- `SellerProfile`: display name, bio, location, response time, rating summary, verification state
- `SellerOnboarding`: provider account references, KYC state, requirements, review timestamps
- `PortfolioItem`: seller media, title, description, moderation state

### Catalog

- `Category`
- `Tag`
- `Gig`: title, slug, description, seller, status, moderation state, search fields
- `GigPackage`: package name, price, delivery time, revisions, scope
- `GigAddon`: optional add-on name, price, delivery impact, availability
- `GigMedia`
- `Favorite`
- `Availability` or seller capacity settings

### Orders

- `Order`: buyer, seller, gig/package snapshot, status, deadlines, acceptance timestamps
- `OrderItem`: immutable purchased package/add-on details and amounts
- `OrderRequirement`: buyer-provided inputs and completion status
- `OrderMessage`
- `OrderAttachment`
- `Delivery`: submitted work, version, timestamp, status
- `RevisionRequest`
- `CancellationRequest`
- `Dispute`: reason, evidence, decision, amounts, resolution timestamps
- `Review`: rating, text, moderation state, order linkage
- `DraftOrder`: multi-step checkout progress stored server-side so a browser can be refreshed at any step

### Payments and Accounting

- `PaymentIntent`: order, provider, method, amount, currency/asset, status, expiration
- `PaymentAttempt`: provider transaction IDs, raw status, timestamps, failure details
- `PaymentWebhookEvent`: provider, event ID, payload hash, processing status, retry metadata
- `Refund`: requested, approved, provider reference, amount, status
- `LedgerAccount`
- `LedgerEntry`: immutable double-entry postings for buyer payment, platform fee, seller payable, refunds, adjustments, and payouts
- `Payout`: seller, destination, provider, amount, status, provider reference
- `FeeSchedule`
- `ExchangeRateSnapshot` for fiat/crypto display and conversion records

### Operations

- `Notification`
- `ModerationCase`
- `AdminAction`
- `AuditLog`
- `FeatureFlag` or platform configuration records

Orders, payment intents, ledger entries, and webhook events must be append-only or state-transition controlled. Do not overwrite financial history.

## 8. Order and Funds Lifecycle

1. Buyer selects a gig package and add-ons.
2. Server calculates the authoritative subtotal, platform fee, seller amount, taxes or required jurisdictional charges, and total.
3. Server creates a `DraftOrder`, then walks the buyer through a multi-step checkout form (requirements, payment method, review, confirm).
4. On confirm, the server creates an order in `pending_payment` and a payment intent for the selected provider.
5. Buyer is redirected to the provider-hosted checkout page, or shown a server-generated crypto deposit address and QR code.
6. Provider webhook is verified, deduplicated, and processed asynchronously by the worker.
7. The payment intent transitions to `paid` only after server-side confirmation meets the provider’s settlement policy.
8. A ledger transaction records gross buyer funds, platform fees, and seller payable funds.
9. The order transitions to `in_progress` and the seller receives the requirements.
10. Seller submits a delivery. Buyer can accept, request a revision, or open a dispute.
11. On acceptance, or after an explicitly configured auto-accept period, the seller payable becomes eligible for release.
12. Payout processing sends eligible funds to the seller’s connected account or approved external wallet.
13. Payout confirmation creates the corresponding ledger postings and closes the payable.

Define all allowed order transitions in one state machine. Prevent delivery, acceptance, refund, and payout actions from being inferred solely from client state.

Recommended initial order states:

`pending_payment`, `payment_failed`, `paid`, `in_progress`, `delivered`, `revision_requested`, `accepted`, `disputed`, `cancel_requested`, `cancelled`, `payout_pending`, `paid_out`, and `closed`.

## 9. Payment Provider Architecture

Create a common adapter interface in `providers`:

```go
type Provider interface {
    CreatePayment(ctx context.Context, in CreatePaymentInput) (PaymentSession, error)
    Payment(ctx context.Context, id string) (NormalizedPayment, error)
    Refund(ctx context.Context, in RefundInput) (RefundResult, error)
    VerifyWebhook(ctx context.Context, r *http.Request) (VerifiedEvent, error)
}
```

Provider adapters should emit normalized statuses and events. Store the original provider reference and selected non-sensitive metadata for reconciliation, but do not treat provider payloads as the internal accounting system.

### Hosted Checkout Requirement

Because the front end ships no JavaScript, all checkout flows must work through provider-hosted pages and server-side redirects:

- **Stripe**: Stripe Checkout Sessions; redirect the buyer to the hosted page and return via `success_url`/`cancel_url`.
- **BTCPay**: per-order invoices; redirect the buyer to the hosted invoice page.
- **Stablecoins**: generate a per-order deposit address (or unique payment reference) server-side, render a server-generated QR code and the destination details as plain HTML, and confirm via backend indexer jobs. No wallet-connect browser extension or dApp UI is used.

### Stripe Connect

- Use Stripe Connect Express or Custom accounts based on the eventual country and compliance requirements.
- Support seller onboarding through Stripe-hosted or embedded onboarding rather than collecting sensitive KYC data directly.
- Use Checkout Sessions for buyer payments and destination charges or separate charges and transfers only after confirming the desired hold/release model and Stripe country support.
- Use application fees for the platform fee where compatible with the selected charge model.
- Treat Stripe webhooks as authoritative for payment success, failure, refund, dispute, account, and payout events.
- Never expose secret keys or trust client-supplied prices.

### BTCPay Server

- Integrate through the BTCPay API using per-order invoices.
- Support Bitcoin on-chain and Lightning where the configured BTCPay instance and wallet support both.
- Persist invoice ID, payment destination metadata, requested amount, currency conversion snapshot, required confirmations, and expiry.
- Define when an invoice is considered usable for fulfillment: paid, confirmed, or fully confirmed based on risk policy.
- Process invoice and settlement webhooks idempotently.
- Handle underpayment, overpayment, expiration, partial payment, and refund workflows explicitly.
- Avoid holding private keys in the application. Use BTCPay wallet infrastructure or a dedicated custody arrangement that has been reviewed for the target jurisdiction.

### Stablecoins on an EVM Network

- Select one network for launch, such as Base or Polygon, after reviewing fees, liquidity, stablecoin support, RPC reliability, and regulatory constraints.
- Prefer a hosted or custody-aware payment processor for the first implementation unless the platform has a reviewed smart-contract and wallet operations team.
- If using direct wallet payments, generate unique deposit addresses or payment references per order where possible.
- Record chain ID, token contract address, transaction hash, sender, recipient, token amount, block number, and confirmation threshold.
- Verify token contract addresses from configuration, not user input.
- Wait for the configured confirmation count and protect against reorgs before marking an order paid.
- Add a reconciliation job that scans provider/indexer data for missed webhooks.
- Establish a separate operational design for gas funding, treasury management, key custody, and emergency pause procedures.

### External Wallet Payouts

- Require seller verification and wallet ownership confirmation before enabling withdrawals.
- Store wallet addresses encrypted or otherwise protected, with network and asset explicitly bound to the destination.
- Require a fresh confirmation for address changes and apply a security cooling-off period.
- Use allowlists, transaction limits, manual review thresholds, and an admin emergency pause.
- Never send a payout based only on a client-provided wallet address.

## 10. Authentication and Sessions

- Server-side sessions stored in PostgreSQL, referenced by a random high-entropy token held in a secure, `HttpOnly`, `SameSite=Lax` cookie.
- Use `SameSite=Strict` for session cookies where redirect-based flows permit, with a permissive exception only for payment return paths.
- Session table stores the token hash (never the raw token), expiry, user agent, IP, and revocation timestamp.
- Rotate the session on privilege change (login, password change, role change) and provide logout that revokes server-side.
- Password hashing with argon2id; constant-time comparisons for all secrets and webhook signatures.
- Email verification, password reset, and optional TOTP MFA, all implemented with server-rendered forms.
- Every state-changing form carries a CSRF token bound to the session; all handlers verify it.

## 11. Multi-Step Journeys and Uploads

All user journeys are multi-step HTML form sequences. Progress is stored server-side in a `DraftOrder` or draft profile record, so a refresh never loses the step.

Checkout example:

1. Review gig and totals (GET)
2. Requirements form (POST -> 303 to next step)
3. Payment method selection (POST)
4. Review and confirm (POST -> creates order + payment intent)
5. Redirect to hosted checkout, or render deposit instructions with QR (GET, meta-refresh for crypto status)

Uploads:

- Standard multipart `enctype="multipart/form-data"` forms.
- Server validates type, size, and content; files are stored through the storage interface and attachments are referenced by ID.
- Delivery evidence, portfolio images, and dispute evidence follow the same path.
- Multi-file uploads are handled as stepwise form submissions (one item per step or an enumerated list) since no JavaScript is allowed.

## 12. Fees, Ledger, and Reconciliation

- Define a versioned fee schedule with buyer/seller/platform responsibility clearly documented.
- Calculate and persist fee snapshots at order creation so later configuration changes do not alter existing orders.
- Use immutable double-entry ledger postings for every money movement.
- Maintain separate balances for pending seller earnings, available seller earnings, platform revenue, refunds, reserves, and provider clearing accounts.
- Reconcile internal payment records against Stripe, BTCPay, and the EVM payment source on a scheduled basis.
- Make reconciliation exceptions visible in the admin console.
- Support manual adjustments only through audited, permissioned actions that require a reason.
- Keep provider fees, network fees, exchange-rate spreads, and platform fees separately identifiable.
- Document accounting, tax, custody, and money-transmission implications with qualified local counsel before production launch.

## 13. API Surface and Handlers

Handlers live in `handlers/` grouped by concern. All input is validated server-side; nothing is trusted from the client.

### Public Marketplace

- Browse categories and featured gigs
- Search and filter gigs
- View gig details and seller profiles
- View public reviews and portfolio items

### Authenticated Buyer

- Start and progress through checkout
- Submit requirements
- View orders and deliveries
- Send messages
- Request revisions, acceptance, cancellation, refunds, and disputes
- Create reviews

### Authenticated Seller

- Manage profile, portfolio, and gigs
- Manage order requirements and deliveries
- Send messages
- View earnings and payout eligibility
- Start or continue provider onboarding
- Manage approved payout destinations

### Admin

- Moderate users, profiles, gigs, media, reviews, and messages where permitted
- Search and inspect orders and payment events
- Resolve disputes and approve exceptional refunds
- Manage settings, fees, networks, and provider availability
- Export reports and inspect audit logs

Protect endpoints with authentication, authorization, schema validation, rate limits, idempotency keys for money-changing requests, and CSRF protection.

## 14. Mobile-First UX Without JavaScript

Use a bottom navigation pattern for the primary mobile surfaces:

- Home / discover
- Search
- Orders
- Messages
- Account

Prioritize:

- Large tap targets and thumb-reachable actions
- Sticky checkout summary and clear payment-method selection
- Simple order status timeline
- Delivery review with prominent accept, revise, and dispute actions
- Compact seller earnings and payout status cards
- Upload flows that work reliably on mobile networks
- Skeleton/loading states, empty states, and actionable errors
- Accessible forms, keyboard navigation, contrast, labels, and screen-reader structure
- Server-side pagination and search filters on every listing

Because there is no JavaScript, every interaction is a navigation:

- Follow the Post/Redirect/Get pattern for every state change to prevent duplicate submissions.
- On validation errors, re-render the form with a `403`-style error summary and per-field errors linked to the relevant `aria-describedby` hints.
- Use `autocomplete`, `inputmode`, `enterkeyhint`, and correct input `type` values to give mobile keyboards the right layout.
- Use `<meta http-equiv="refresh">` only for payment-pending and confirmation-count status pages.
- Keep the full page weight small and render server-side quickly; navigation should feel instant on mobile.

Desktop should expand the same information architecture rather than create a separate product model.

## 15. Admin and Operations

Build operational views early enough that payment testing and dispute handling are not dependent on database access.

Required admin capabilities:

- Search by user, order ID, payment ID, provider reference, wallet address, and transaction hash
- View complete order and payment timelines
- Retry failed webhook processing safely
- Inspect reconciliation mismatches
- Place accounts, gigs, orders, or payouts on hold
- Review and resolve disputes with evidence and an internal decision note
- Approve exceptional refunds and ledger adjustments
- View payout queues and failed payouts
- Export CSV reports with sensitive-field access controls
- Record every privileged action in an audit log

## 16. Background Jobs and Reliability

- A PostgreSQL-backed job queue stores retries, dead-letter state, and ownership.
- Worker goroutines claim jobs with `SELECT ... FOR UPDATE SKIP LOCKED` and process with backoff.
- Webhook processing, reconciliation, payout attempts, order auto-accept, payment-expiry, and notification dispatch all run as jobs.
- All webhook processing is idempotent by provider event ID.
- Scheduled reconciliation compares internal state against provider records and surfaces exceptions.
- Dead-lettered jobs are visible to administrators with retry and inspection tools.

## 17. Security and Compliance Work

- Use secure authentication with email verification, password reset, optional MFA, and session revocation.
- Encrypt secrets using deployment secret management; never commit provider keys.
- Verify all webhook signatures and reject replayed or stale events.
- Use idempotency at checkout, webhook, refund, payout, and ledger boundaries.
- Apply rate limiting to authentication, messaging, checkout, uploads, and wallet changes.
- Scan uploaded files, restrict types and sizes, and serve private attachments through signed URLs.
- Redact payment and identity data from logs.
- Send strict security headers: CSP with `script-src 'none'`, `Referrer-Policy`, `X-Content-Type-Options`, `X-Frame-Options`, and `Permissions-Policy`.
- Use `SameSite`, `HttpOnly`, and `Secure` cookie attributes.
- Add fraud controls for velocity, suspicious order patterns, chargebacks, wallet changes, and high-value transactions.
- Define data retention, deletion, export, consent, and privacy policies for the operating country.
- Complete KYC/KYB, sanctions screening, AML, money-transmission, consumer-protection, tax, chargeback, and crypto-custody reviews with qualified counsel and compliance providers.
- Do not launch seller payouts or direct custody until provider and legal requirements are satisfied.

## 18. Testing Strategy

### Unit Tests

- Fee and total calculations
- Currency and crypto amount conversion
- Order state transition rules
- Seller payout eligibility
- Refund and dispute calculations
- Ledger balancing
- Webhook signature and event normalization
- Component rendering output (semantic structure, escaping, attribute presence)

### Integration Tests

- Database transactions and constraints
- Authentication, sessions, CSRF, and authorization
- Checkout creation for every provider adapter (against provider sandboxes)
- Webhook deduplication and retry behavior
- Payment-to-order transitions
- Acceptance-to-payout transitions
- Refund and cancellation behavior
- Wallet verification and payout holds
- Multi-step journey state persistence (draft orders)

### End-to-End Tests

- Buyer registration through completed order
- Seller onboarding through gig publication
- Fiat payment in Stripe test mode
- BTCPay test invoice through webhook confirmation
- Stablecoin testnet payment through confirmation threshold
- Delivery, revision, acceptance, and payout
- Dispute and administrator resolution
- Mobile viewport flows and accessibility checks
- Confirmation that no page contains `<script>` and all pages work with JavaScript disabled

Browser-level end-to-end tooling runs only in the development/test environment; it is never shipped to users.

### Reliability Tests

- Duplicate and out-of-order webhooks
- Provider downtime and retry queues
- Expired payment sessions
- Partial, under, and overpayments
- Blockchain reorg or insufficient confirmations
- Job queue retry and dead-letter handling
- Concurrent acceptance/refund/payout attempts
- Migration startup under contention (advisory lock behavior)

## 19. Delivery Phases

### Phase 0: Product, Legal, and Provider Decisions

- Select operating country and initial seller countries.
- Confirm marketplace business model, fee model, currencies, tax responsibilities, and seller-of-record responsibilities.
- Confirm Stripe Connect availability and account type.
- Provision a BTCPay Server test environment.
- Select the stablecoin network, settlement asset, indexer/provider, and custody model.
- Define refund, escrow-like hold, auto-accept, dispute, and payout policies.
- Produce threat model, data classification, and compliance checklist.

### Phase 1: Foundation

- Scaffold the Go module, flat layout, `main.go`/`server.go`/`routes.go` split, and `APP_ROLE` dispatch.
- Configure `docker-compose.yml` with `db`, `web`, and `worker` on an internal network.
- Implement embedded startup migrations with advisory locking and schema-version checks.
- Implement config validation, structured logging, health endpoints, and graceful shutdown.
- Add `static/app.css`, the layout component, semantic base templates, and the mobile shell.
- Set up linting, formatting, testing, and CI.

### Phase 2: Identity and Sessions

- Implement argon2 password hashing, email verification, password reset, and TOTP MFA.
- Implement PostgreSQL-backed cookie sessions with rotation and revocation.
- Implement roles, permissions, CSRF protection, security headers, and rate limiting.
- Implement audit logging and basic account settings.

### Phase 3: Marketplace Core

- Implement seller profiles and portfolio uploads via multipart forms.
- Implement categories, tags, gig creation, packages, add-ons, availability, and moderation status.
- Implement browse, search, filters, sorting, favorites, and public seller/gig pages.
- Implement buyer and seller dashboards.

### Phase 4: Orders and Messaging

- Implement authoritative pricing and multi-step checkout with `DraftOrder` persistence.
- Implement requirements, order workspace, messages, attachments, deliveries, revisions, and reviews.
- Implement the complete order state machine and notification events.
- Implement cancellation and dispute records before connecting real payouts.

### Phase 5: Fiat Payments and Seller Onboarding

- Implement the provider interface and Stripe adapter.
- Implement Stripe Connect seller onboarding and account status handling.
- Implement Stripe Checkout sessions, webhooks, refunds, and payout events.
- Implement ledger postings and reconciliation for Stripe test mode.
- Add admin payment inspection tools.

### Phase 6: Bitcoin and Lightning

- Implement BTCPay invoice creation and hosted checkout redirect.
- Implement webhook verification, invoice state mapping, confirmation policy, and expiry handling.
- Implement underpayment, overpayment, and refund handling.
- Add reconciliation jobs and admin visibility.

### Phase 7: Stablecoin Payments and Wallet Payouts

- Add the selected EVM network adapter or payment processor adapter.
- Implement per-order deposit addresses, server-generated QR codes, transaction verification, confirmations, reorg-safe reconciliation, and refund policy.
- Implement seller wallet verification, cooling-off period, payout queue, limits, and manual review.
- Add operational controls for pause, allowlists, treasury, gas, and key custody.

### Phase 8: Full Operations and Hardening

- Complete moderation, dispute, payout, reconciliation, and reporting dashboards.
- Add fraud/risk rules, alerts, rate limits, file scanning, and security monitoring.
- Perform accessibility, mobile performance, load, backup restore, and disaster-recovery testing.
- Verify no shipped page contains script tags and all journeys work with JavaScript disabled.
- Run provider sandbox certification and end-to-end payment drills.
- Complete legal/compliance sign-off and production runbooks.

### Phase 9: Controlled Launch

- Launch with a limited seller cohort and low transaction limits.
- Monitor payment success, webhook failures, reconciliation exceptions, refunds, disputes, and payout delays.
- Keep stablecoin and external-wallet payouts behind feature flags until operationally proven.
- Expand countries, currencies, networks, and payout limits only after measured review.

## 20. Environment Configuration

Use environment validation and separate credentials per environment. Expected configuration categories include:

- `DATABASE_URL` for PostgreSQL on the internal Docker network
- `APP_ROLE` to select the web or worker entrypoint
- Authentication, session, and CSRF secrets
- File storage credentials and bucket names
- Email and notification provider credentials
- Stripe secret, publishable, webhook, and Connect configuration
- BTCPay URL, store ID, API credentials, webhook secret, and settlement policy
- EVM chain ID, RPC/indexer credentials, token contract addresses, confirmation count, and payout controls
- Encryption keys for protected payout and provider metadata
- Fee schedule, supported currencies/assets, limits, and feature flags

Provide `.env.example` with placeholders only. Do not commit live credentials, wallet private keys, seed phrases, webhook secrets, or identity documents.

## 21. Launch Acceptance Criteria

- A buyer can purchase a gig using each enabled payment method in sandbox/testnet environments.
- No order becomes paid from client-side input alone.
- Duplicate, delayed, and out-of-order webhooks do not create duplicate ledger entries or payouts.
- Every successful payment produces balanced ledger postings.
- Seller funds remain unavailable until acceptance or configured auto-acceptance.
- Refunds, cancellations, disputes, and payout failures are visible and recoverable from the admin console.
- Seller payout destinations are verified and protected by cooldowns and limits.
- Payment, wallet, and identity secrets are absent from client output and logs.
- No shipped page contains `<script>`, inline handlers, or third-party widgets.
- All checkout, upload, and order journeys work end to end with JavaScript disabled.
- Mobile checkout, order fulfillment, delivery review, and dispute flows are usable at narrow viewport sizes.
- Automated tests cover critical financial and state-transition paths.
- Migrations apply cleanly at startup under a single instance and under concurrent instances.
- Backup restoration, provider outage, webhook replay, and reconciliation procedures have been exercised.
- Operating-country legal, compliance, tax, and provider approval requirements are documented and signed off before production funds are accepted.
