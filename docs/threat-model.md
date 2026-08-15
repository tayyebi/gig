# Threat Model

Status: **DRAFT**, produced from the current implementation
(`main`@this commit) for Phase 0/Phase 8 sign-off. Uses a lightweight
STRIDE-per-component pass rather than a full formal model, scoped to what
this codebase actually does.

## Assets

1. Buyer and seller PII (email, name, session data, IP/UA on sessions).
2. Payment credentials and provider secrets (Stripe secret key, BTCPay API
   key, EVM RPC URL, wallet encryption key) — never buyer card data itself,
   since checkout is provider-hosted.
3. Seller payout wallet addresses (encrypted at rest).
4. The ledger (`ledger_accounts`/`ledger_entries`) — the authoritative
   record of who owns what money.
5. Order/message/dispute content (may include personal information
   exchanged between buyer and seller).
6. Admin session and audit trail integrity.

## Trust boundaries

- Browser ↔ web server: untrusted input, zero JS, CSRF-protected forms.
- Web/worker ↔ PostgreSQL: internal Docker network only, no external
  exposure (`docker-compose.yml`).
- Web/worker ↔ payment providers: outbound HTTPS calls plus inbound
  webhook HTTP requests, each independently signature-verified per
  provider (`providers/*.go` `VerifyWebhook`).
- Web/worker ↔ blockchain RPC endpoints: outbound only, read-only calls
  (`eth_getLogs`/`eth_blockNumber`); the platform never holds a signing key
  for on-chain transfers.

## STRIDE by component

### Authentication / sessions (`sessions.go`, `handlers/auth.go`)

- **Spoofing**: mitigated by argon2id password hashing, TOTP MFA,
  constant-time comparisons, session rotation on privilege change.
- **Tampering**: session tokens are opaque, hashed at rest
  (`sessions` table stores a hash, not the raw token); CSRF tokens gate
  every state-changing form.
- **Repudiation**: `AuditLog` records privileged actions with actor,
  action, target, and metadata.
- **Information disclosure**: session cookies are `HttpOnly`, `Secure`,
  `SameSite=Lax`; `logger.go` redacts sensitive fields from structured
  logs.
- **Denial of service**: `services.RateLimiter` gates auth, messaging,
  checkout, uploads, and wallet-change endpoints.
- **Elevation of privilege**: role checks (`requireSeller`,
  `requireRole(store.RoleAdmin, ...)`) are enforced server-side at route
  registration, never trusted from client state.

**Residual risk**: no CAPTCHA or device-fingerprinting layer against
credential-stuffing beyond rate limiting and lockout
(`MaxLoginAttempts`/`LoginLockout`). Acceptable for initial launch scale;
revisit if abuse is observed.

### Payments (`providers/*.go`, `payments.go`, `handlers/payments.go`)

- **Spoofing a webhook**: every provider adapter verifies signatures
  (Stripe HMAC, BTCPay `BTCPay-Sig` HMAC, both with a 5-minute timestamp
  tolerance) before the event is trusted; `ParseEvent` (used by the async
  job) never re-verifies, by design, because the job only ever processes
  events that were already verified and persisted at receipt time —
  an attacker cannot get an unverified payload into the job queue.
- **Tampering with order totals**: totals are computed server-side and
  snapshotted into `OrderItem` at order creation; nothing about price is
  ever trusted from client input on the payment-confirmation path.
- **Replay**: webhook events are deduplicated by `(provider, event_id)`
  (`idx_webhook_events` unique constraint,
  `TestInsertWebhookEventDedup`).
- **Race conditions**: order/payment-intent state transitions are guarded
  by `WHERE ... AND status = $expected` (never a blind `SET`), verified
  under concurrency (`TestConcurrentClaimJobOnlyOneWinner`,
  `TestConcurrentTransitionPayoutOnlyOneWinner`).
- **Double-refund**: `idx_refunds_order_not_failed` plus an
  application-level pre-check (`GetRefundForOrder`) prevent a
  double-submitted refund from creating two refund rows or double-posting
  ledger entries.

**Residual risk**: no chargeback/dispute webhook parsing yet for Stripe
(flagged in TODO.md); a chargeback today is only caught by manual admin
review of Stripe's own dashboard, not surfaced automatically in this app.

### Wallet payouts (`services/walletcrypto.go`, `store/wallets.go`)

- **Tampering**: wallet addresses are AES-256-GCM encrypted at rest, scoped
  by (user, network, asset); a wallet change requires a fresh
  email-confirmed row plus a cooling-off period before it's payout-eligible
  — an attacker who compromises a session cannot redirect payouts
  instantly.
- **Elevation of privilege**: payouts always resolve the destination
  address from the stored, confirmed wallet row via `wallet_id` — never
  from client-supplied address text at payout time.
- **Information disclosure**: the encryption key
  (`WALLET_ENCRYPTION_KEY`) is a required env var, never logged
  (`redactAttr` masks any log field whose key contains "wallet").

**Residual risk**: the encryption key itself, if compromised, decrypts
every stored wallet address. Key rotation is not implemented; a rotation
runbook is needed before this scales past the initial pilot (see
`docs/runbooks/treasury-custody.md`).

### File uploads (`services/storage.go`)

- **Tampering / malicious upload**: content-type sniffing, size caps, an
  EICAR signature check, and rejection of executable/script extensions
  inside zip archives (`scanUpload`).
- **Information disclosure**: order attachments and dispute evidence live
  outside the public `/media/` mount, served only after an
  buyer/seller/admin ownership check; portfolio/gig media is public by
  design.

**Residual risk**: `scanUpload`'s malware check is a lightweight signature
check, not a real AV engine — explicitly noted in TODO.md as out of scope
for this pass. Acceptable for launch given file types are constrained
(images/PDFs for portfolios, order deliveries); revisit if the platform
starts accepting arbitrary executable-adjacent file types.

### Admin console (`handlers/admin.go`)

- **Elevation of privilege**: every `/admin/*` route requires
  `store.RoleAdmin`; every privileged action is CSRF-protected and
  audit-logged (including CSV exports, which log the row count exported).
- **Repudiation**: `/admin/audit` and `/admin/audit/export.csv` give a
  reviewable trail of every admin action.

**Residual risk**: no admin-side MFA *requirement* is enforced beyond what
the general account-settings MFA enrollment offers — an admin account
compromised via a non-MFA path has full admin console access. Recommend
requiring MFA enrollment specifically for accounts holding `RoleAdmin`
before production launch (not currently enforced in code).

## Out of scope for this document

Physical/hosting-provider security, employee access controls to
production infrastructure, and legal/compliance review (KYC/AML,
sanctions screening) are covered by `docs/phase0-decisions.md`'s
Compliance section and Phase 8/9 of TODO.md, not this threat model.
