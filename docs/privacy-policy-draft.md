# Privacy Policy — Draft for Legal Review

Status: **DRAFT, NOT FOR PUBLICATION**. This is a starting point written
from the engineering side (what data the system actually collects and
why), for legal counsel to turn into a publishable policy. It is not
legal advice and must not be published as-is.

## 1. What we collect

- **Account data**: name, email address, password (stored as a one-way
  hash, never in plaintext), and optionally a TOTP secret if you enable
  two-factor authentication.
- **Session data**: a session identifier, the IP address and browser user
  agent associated with each login, for security purposes (detecting
  suspicious logins, enabling you to review/revoke active sessions).
- **Order and messaging data**: the content of gig requirements, delivery
  files, and messages exchanged between a buyer and seller for a specific
  order.
- **Payment data**: we do not collect or store your card number. Card
  payments are processed by Stripe directly; we store only the resulting
  transaction reference and status. For cryptocurrency payments, we
  observe public blockchain data (transaction hashes, wallet addresses)
  necessary to confirm your payment.
- **Seller payout data**: if you are a seller receiving cryptocurrency
  payouts, your payout wallet address is stored encrypted and is never
  shared publicly.
- **Audit data**: administrative actions (e.g. account suspension, refund
  issuance) are logged with the acting admin's identity, for accountability
  and fraud investigation.

## 2. Why we collect it

- To operate your account and process your orders.
- To detect and prevent fraud, account takeover, and abuse.
- To comply with financial recordkeeping obligations.
- To communicate with you about your orders (transactional email only —
  we do not send marketing email from this platform today).

## 3. Who we share it with

- **Stripe** (card payment processing and seller payouts), **BTCPay
  Server** (Bitcoin/Lightning payment processing, self-hosted or
  third-party hosted depending on deployment), and the **blockchain
  networks** used for stablecoin payments (Base, Polygon) — transactions
  on these networks are public by nature.
- We do not sell personal data to third parties.
- We may disclose data if legally required (subpoena, court order) or to
  investigate suspected fraud or abuse of the platform.

## 4. How long we keep it

See `docs/data-classification.md`'s Retention section. In short: order and
financial records are kept indefinitely for accounting purposes even after
account deletion; personal identifiers are anonymized on a verified
deletion request where legally permitted (**this anonymization flow is not
yet built** — flagged in `docs/data-classification.md`).

## 5. Your choices

- You can review and revoke active sessions from your account settings.
- You can request account deletion (**self-service deletion is not yet
  implemented**; today this requires contacting support directly — a gap
  to close before this policy can be published, since most privacy
  regimes require a self-service or low-friction deletion path).

## 6. Open items before this can be published

1. Self-service data export and deletion request handling.
2. A named data protection contact / process for handling requests.
3. Jurisdiction-specific addenda (GDPR if EU sellers/buyers are in scope,
   CCPA if California residents are, per the Phase 0 country decision in
   `docs/phase0-decisions.md`).
4. Sub-processor list finalized (exact vendors, not just categories).
5. Cookie disclosure — this app sets only strictly necessary cookies
   (session, CSRF) and no analytics/marketing cookies, which simplifies
   consent requirements, but this should still be stated explicitly and
   confirmed by counsel.
