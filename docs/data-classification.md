# Data Classification and Compliance Checklist

Status: **DRAFT**, for Phase 0/Phase 8 sign-off.

## Classification levels

- **Restricted**: would cause direct financial or safety harm if leaked
  (payment provider secrets, wallet encryption key, session token hashes).
- **Confidential**: personal data that identifies a user or enables account
  takeover if leaked (email, password hash, TOTP secret, IP/UA history,
  wallet address).
- **Internal**: operational data not directly tied to an individual's
  identity or funds (order status, gig content, audit log metadata).
- **Public**: content intentionally shown to any visitor (gig listings,
  seller public profile, published reviews).

## Data inventory

| Data | Classification | Where stored | Encrypted at rest? | Notes |
|---|---|---|---|---|
| Password hash | Confidential | `users.password_hash` | argon2id (one-way) | Never logged (`redactAttr`). |
| TOTP secret | Restricted | `users` MFA columns | Not additionally encrypted | Same trust boundary as password hash; consider encrypting if the DB threat model changes. |
| Session token | Restricted | `sessions.token_hash` | Hashed, not reversible | Raw token only ever exists client-side in the cookie. |
| Email address | Confidential | `users.email` | No | Redacted from logs; used for account recovery and transactional email. |
| Wallet address | Restricted | `seller_wallets.address_encrypted` | AES-256-GCM | Key from `WALLET_ENCRYPTION_KEY`; fingerprint (`address_fingerprint`, a SHA-256 hash) stored unencrypted for duplicate detection without decrypting. |
| Payment provider secrets | Restricted | Process env only (`config.go`) | N/A — never persisted to DB | Never logged, never returned in any HTTP response. |
| Ledger entries | Confidential | `ledger_entries` | No | Financial record; access gated to the owning seller and admins. |
| Order requirements / messages | Confidential | `orders.requirements`, `order_messages` | No | May contain personal information exchanged between buyer/seller. |
| Order/dispute attachments | Confidential | Private filesystem root outside `/media/` | No (filesystem-level, not field-level) | Served only after an ownership check. |
| IP address / user agent | Confidential | `sessions`, `audit_log` | No | Used for session security and audit trail, not analytics. |
| Gig listings, portfolio media | Public | `gigs`, `gig_media`, public storage | N/A | Intentionally public. |
| Reviews | Public (once approved) | `reviews` | N/A | Held in `pending` moderation state until approved. |

## Retention

No automated retention/deletion job exists yet (a real gap — flagged
here, not previously called out explicitly in TODO.md). Recommended
defaults, pending legal review:

- Session rows: already time-bound by `SessionTTL`; expired rows should be
  periodically purged by a new job (not yet implemented).
- Audit log: retain indefinitely for financial/compliance record-keeping
  (do not auto-delete).
- Soft-deleted user accounts (`users.status = 'deleted'`): retain
  financial records (orders, ledger) indefinitely for accounting/legal
  purposes even after a user requests deletion; anonymize PII fields
  (email, name) on deletion request rather than deleting the row outright,
  so the ledger and order history stay intact. **Not yet implemented** —
  today `AddRole`/user status changes exist, but there is no "anonymize on
  deletion request" flow. Flagged as a follow-up before this can be called
  GDPR/CCPA-ready.

## Compliance checklist (status against current implementation)

- [x] Passwords hashed with a modern, salted, memory-hard algorithm (argon2id)
- [x] MFA available (TOTP)
- [x] Sessions revocable server-side, rotated on privilege change
- [x] CSRF protection on all state-changing forms
- [x] Secrets never logged or returned in client-facing output
- [x] Payment card data never touches this application (hosted checkout only)
- [x] Audit trail for privileged actions
- [ ] Data subject access/export request handling (no dedicated flow; the
      CSV export tooling is admin-facing, not a self-service user export)
- [ ] Right-to-erasure / anonymization flow (see Retention section above)
- [ ] Sanctions/OFAC screening integration (Phase 8 Compliance section,
      TODO.md — needs a real vendor)
- [ ] Formal data processing agreement with each sub-processor (Stripe,
      BTCPay host, RPC provider) — a legal/contracts task, not code
- [ ] Cookie/consent banner and published privacy policy — see
      `docs/privacy-policy-draft.md` for a starting draft; publishing
      requires legal sign-off
