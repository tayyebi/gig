# Deferred External Items Ledger

Status: formally ratified as deferred by the project owner on 2026-08-15.
Engineering implementation of `PLAN.md`/`TODO.md` is complete; every item
in this ledger requires a real vendor, real legal counsel, a real deployed
environment, a real device/browser, or real live production traffic —
categorically outside what a code change can produce. This document
exists so each one has a clear, concrete "what closes this" definition
instead of sitting as vague, indefinitely-open engineering work.

## Real infrastructure to provision or exercise

| Item | What closes it |
|---|---|
| BTCPay Server test environment | Stand up a BTCPay Server instance (self-hosted or hosted) against Bitcoin testnet/signet, then point `BTCPAY_*` config at it. `providers/btcpay.go` is complete and untested only for lack of a live endpoint. |
| Backup and restore exercise | Follow `docs/runbooks/backup-restore.md` against a real deployed PostgreSQL instance (or staging copy), verify restore integrity, record the result and any procedure corrections. |
| Disaster-recovery exercise | Follow `docs/runbooks/disaster-recovery.md` against a real deployment, ideally alongside the backup/restore exercise. |
| Provider sandbox certification and end-to-end payment drills | Run real transactions through Stripe test mode, BTCPay testnet, and an EVM testnet against a deployed instance of this application, confirming the full webhook → ledger path in a live network environment (this session validated the same path against a local database with a stub provider — `webhook_ledger_test.go` — but not against a live provider sandbox over the network). |

## Real device / browser / assistive technology

| Item | What closes it |
|---|---|
| Focus order and keyboard-trap audit | A person tabbing through every major page/journey in a real browser, confirming focus order matches visual/reading order and nothing traps keyboard focus. Not mechanically checkable from markup alone (unlike the label/alt-text/contrast/landmark checks already automated in `components/a11y_test.go` and `contrast_test.go`). |
| Screen-reader announcement behavior | A person using a real screen reader (VoiceOver, NVDA, JAWS) through the major journeys, confirming announcement order and clarity. Semantic HTML structure is already verified automatically; actual assistive-technology *behavior* is not. |
| On-device mobile performance | Real-device testing (actual phone hardware, real network conditions e.g. throttled 3G/4G) for perceived load time and interaction responsiveness. Page-weight budgets are already automated (`components/pageweight_test.go`); CPU/render performance on real hardware is not. |

## Real vendors and legal review

| Item | What closes it |
|---|---|
| KYC/KYB and sanctions screening integration | Select and integrate a real KYC/sanctions-screening vendor (e.g. Persona, Sumsub, ComplyAdvantage) into seller onboarding and/or buyer checkout, per whatever the ratified "global, no country restriction" posture (`docs/phase0-decisions.md`) requires for sanctions compliance. |
| AML, money-transmission, and consumer-protection review | Legal counsel review of whether this platform's payment-facilitation model triggers money-transmitter licensing in any operating jurisdiction, given the ratified marketplace-of-record fee model. |
| Tax and chargeback process review | Legal/finance counsel review of the ratified "seller handles their own tax reporting" stance and Stripe's chargeback-handling defaults, particularly given the "global, no country restriction" decision. |
| Crypto-custody and treasury review | A security/custody specialist review of the ratified "no platform custody, manual treasury execution" model (`docs/runbooks/treasury-custody.md`) before real treasury funds are handled at scale — specifically evaluating the multi-sig recommendation in that runbook. |
| Legal/compliance sign-off | Formal legal review and sign-off on the Terms of Service, Privacy Policy (`docs/privacy-policy-draft.md` is a starting draft, not publishable as-is), and the ratified Phase 0 decisions as a whole. |

## Live production operations (Phase 9)

These are inherently post-deployment activities — they describe *how to
operate* the platform once it is live, not something achievable before a
live deployment exists:

- Enable the platform for a limited seller cohort.
- Set low transaction and payout limits for the initial cohort.
- Keep stablecoin and external-wallet payouts behind a feature flag
  (`platform_settings` already supports adding an arbitrary key/value pair
  for this — `feature_stablecoin_payouts` is already seeded as an example
  — so the mechanism exists; flipping it on for controlled rollout is an
  operations action, not a code change).
- Monitor payment success rate, webhook failures, and reconciliation
  exceptions once real traffic exists (the underlying visibility already
  exists: `/admin/payments` shows dead-lettered webhook jobs, the
  reconciliation sweep runs automatically — this item is the human
  practice of watching those surfaces regularly, not building them).
- Monitor refunds, disputes, and payout delays similarly.
- Review fraud/risk alerts (`fraud.*` audit log entries) daily.
- Expand countries, currencies, networks, and payout limits after
  measured review of real launch data — inherently requires that data to
  exist first.

## Why these are listed as "complete" with a `(deferred)` tag in TODO.md

Marking these unchecked indefinitely would misrepresent the engineering
state of this repository: there is no further code, test, or
documentation deliverable available for any item in this ledger. The
`(deferred)` tag in TODO.md, backed by this document, is the honest
representation — implementation is done; what remains is action by people
and systems outside this codebase.
