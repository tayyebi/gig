# Treasury, Gas Funding, and Key Custody Runbook

Status: **DRAFT**. This is the item PLAN.md/TODO.md flags as ops-only and
requiring real infrastructure decisions — this document is the starting
runbook, not a substitute for an actual security review before real funds
are handled at scale.

## Current custody model (as implemented)

- The platform **does not hold a programmatic signing key** for any
  on-chain transfer. `providers/evm.go` only ever reads chain state
  (`eth_getLogs`, `eth_blockNumber`); it never constructs or broadcasts a
  transaction.
- Buyer stablecoin payments go directly to a single configured treasury
  address per chain (`EVM_BASE_TREASURY_ADDRESS` /
  `EVM_POLYGON_TREASURY_ADDRESS`), not a per-order generated address.
- Seller wallet payouts (`store/wallets.go`) reach
  `ready_for_manual_execution` and stop — an admin with access to the
  treasury wallet (outside this application, in whatever wallet
  software/hardware holds that key) manually broadcasts the transfer and
  records the resulting tx hash back into the admin console.
- BTCPay refunds are pull payments the buyer claims, not a platform-signed
  transaction either.

This means: **compromising this application's database or servers cannot,
by itself, move on-chain funds.** The worst a compromise of this codebase
enables is fraudulent *queueing* of a payout request (mitigated by the
manual-review threshold and the requirement that an admin manually verify
and execute every transfer) — not an automatic drain.

## Treasury key custody (outside this codebase — action needed)

This is genuinely an infrastructure/ops decision, not something the
application can enforce:

1. **Recommended**: a multi-signature wallet (e.g. Safe on Base/Polygon)
   requiring at least 2 of 3 authorized signers to execute a treasury
   transfer, rather than a single hot wallet key held by one person.
2. Signers should use hardware wallets, not software/hot wallets, for any
   key that can move treasury funds.
3. Maintain an offline, access-controlled record of signer identities and
   a documented process for rotating a signer (e.g. if someone leaves the
   team).
4. Never store a treasury private key, seed phrase, or hardware wallet PIN
   in this repository, in `.env`, in a password manager shared broadly, or
   in any chat/email. This application's `WALLET_ENCRYPTION_KEY` encrypts
   *seller destination addresses*, not any treasury signing key — they are
   different keys with different blast radii and should never be conflated
   or stored together.

## Gas funding

- Base/Polygon transaction fees are paid from the treasury wallet's own
  native-token (ETH on Base, POL on Polygon) balance, not from this
  application.
- Recommended: a monitoring alert (outside this codebase, e.g. a
  block-explorer watch or a simple cron script hitting the RPC endpoint)
  when the treasury's native-token balance drops below a threshold
  sufficient for ~2 weeks of expected payout volume, so gas never blocks a
  payout queue from clearing.
- This monitoring does not exist yet in this repository — flagged as an
  action item, not implemented, since it's an operational script/alert
  against real infrastructure rather than application logic.

## Emergency response if a treasury key is suspected compromised

1. If multi-sig: immediately have the remaining signers rotate to a new
   Safe with new signers/keys, and sweep remaining funds to the new
   treasury address.
2. If single-key (should not be the launch configuration per the
   recommendation above, but if it is): sweep all funds to a freshly
   generated address immediately, then investigate.
3. Update `EVM_BASE_TREASURY_ADDRESS`/`EVM_POLYGON_TREASURY_ADDRESS` in
   config and redeploy so new buyer payments go to the new address.
4. Treat as a SEV1 security incident (`docs/runbooks/incident.md`).
