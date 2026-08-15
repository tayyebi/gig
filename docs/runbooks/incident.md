# Incident Response Runbook

Status: **DRAFT**, written from the current implementation. Adjust contact
names/escalation paths before relying on this operationally.

## Severity levels

- **SEV1**: payments are broken platform-wide (checkout failing for all
  buyers, or the ledger is producing unbalanced entries), or a security
  incident with confirmed data exposure.
- **SEV2**: one payment provider is degraded (e.g. Stripe webhooks not
  arriving) but others still work; a single seller's payouts are stuck.
- **SEV3**: a non-payment feature is degraded (search, messaging, media
  upload).

## First response (any severity)

1. Check `/healthz` and `/readyz` on both `web` and `worker` roles.
2. Check `/admin/payments` for dead-lettered `payment.webhook_process`
   jobs — a spike there is the fastest signal of a provider webhook
   outage.
3. Check `/admin/audit` for recent `fraud.*` or `settings.updated` entries
   around the incident window — rules out "an admin action caused this."
4. Check application logs (structured JSON to stdout) for the affected
   role; `logger.go` redacts secrets, so logs are safe to share with
   whoever is investigating.

## Payment provider outage (SEV1/SEV2)

1. Confirm via the provider's own status page whether the outage is
   upstream.
2. Webhooks that fail to process are retried with backoff automatically
   (`store.FailJob`); once `attempts >= max_attempts` they dead-letter and
   appear on `/admin/payments` with a **Retry** button
   (`adminJobRetry`/`store.RetryJob`).
3. The reconciliation sweep (`payment.reconcile_sweep`, runs every
   `PAYMENT_RECONCILE_INTERVAL`) re-checks stale intents directly against
   the provider, so a webhook outage self-heals once the provider recovers
   — you do not need to manually replay every event, only the ones that
   dead-lettered before the sweep caught them.
4. If the outage is prolonged, consider disabling that provider's checkout
   option (there is no feature-flag UI for this yet — the mitigation today
   is unsetting that provider's config env var and redeploying, which
   causes `providers.Registry` to simply not register it, and it drops out
   of the checkout method selector automatically).

## Ledger imbalance (SEV1)

1. `ledger.Validate` runs on every posting (`store.PostLedgerEntries`), so
   an imbalance should be structurally impossible from application code —
   treat this as a "someone wrote directly to the DB" or "a bug shipped"
   scenario, not routine.
2. Use `/admin/ledger/adjust` to record a corrective entry, always with a
   reason (it's audit-logged).
3. File a bug against whichever code path produced the imbalance; do not
   patch data without also fixing the root cause.

## Security incident

1. Rotate the affected secret immediately (`WALLET_ENCRYPTION_KEY`,
   Stripe/BTCPay API keys, session signing material) — this requires a
   config change and redeploy, not a DB change.
2. Force-revoke sessions if account takeover is suspected: there is no
   single "revoke all sessions" admin action yet (a gap — the only
   per-user lever today is `adminUserStatus` suspending the account, which
   blocks further logins but does not revoke an already-issued session
   token; a dedicated "revoke all sessions for user" admin action is a
   reasonable follow-up).
3. Review `/admin/audit` for the affected account's activity window.
4. Follow the Data Classification doc's incident-notification obligations
   once legal confirms what's required for the jurisdictions in scope.

## Post-incident

- Write a short postmortem: what happened, blast radius, what fixed it,
  what prevents recurrence.
- If it's a recurring class of failure, turn the fix into a test
  (`store/reliability_extra_test.go` is the natural home for a new
  concurrency/reliability regression test).
