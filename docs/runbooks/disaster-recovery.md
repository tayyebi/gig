# Disaster Recovery Runbook

Status: **DRAFT, NOT YET EXERCISED**. Same caveat as
`docs/runbooks/backup-restore.md`: this describes the plan; TODO.md's
"exercise disaster-recovery runbook" item requires actually running a DR
drill against real infrastructure, which is outside what this repository
can do on its own.

## Scenarios covered

1. Database host/instance total loss.
2. Application host/container platform total loss (database survives).
3. Region/provider-wide outage.
4. Accidental destructive operation (e.g. a bad migration, an
   over-broad `DELETE`) against the live database.

## RTO/RPO targets (proposed — confirm with the business)

- **RPO (data loss tolerance)**: no more than the interval between backup
  snapshots — if backups run nightly, up to 24 hours of data loss in the
  worst case. Continuous WAL archiving (see backup-restore runbook)
  reduces this significantly and is recommended once transaction volume
  makes 24 hours of loss unacceptable.
- **RTO (time to restore service)**: proposed target of 4 hours for
  scenario 1/2, 24 hours for scenario 3 (region failover requires
  provisioning a full new environment). These are starting proposals, not
  commitments — confirm against what the business actually needs before
  publishing an SLA anywhere.

## Scenario 1: database total loss

1. Follow `docs/runbooks/backup-restore.md`'s restore procedure against a
   freshly provisioned database instance.
2. Update `DATABASE_URL` for `web` and `worker` to point at the restored
   instance and redeploy.
3. Payments in flight at the time of loss: the reconciliation sweep
   (`docs/runbooks/reconciliation.md`) will re-check any payment intent
   that was `pending`/`processing` at backup time against the provider
   directly once the application is back up, so in-flight payments
   self-heal rather than needing manual intervention — as long as the
   backup wasn't so stale that the intent's `provider_ref` predates the
   restore point (unlikely given normal checkout timing, but worth
   checking for any transaction that straddled the loss).

## Scenario 2: application host/platform loss, database intact

1. Redeploy the `web` and `worker` containers to a new host/platform,
   pointed at the same (still-live) `DATABASE_URL`.
2. `store.Migrate`'s advisory lock means multiple instances starting up
   concurrently during a redeploy is already safe (`TestMigrateUnderContention`)
   — no special handling needed here.
3. Verify `/healthz`/`/readyz` on the new deployment before routing
   traffic to it.

## Scenario 3: region/provider outage

1. Requires a warm or cold standby in a second region — **not currently
   provisioned**; this is an infrastructure investment decision, not
   something the application code enables or blocks. Flagged here as an
   explicit gap rather than assumed to exist.
2. If a standby exists: promote the standby database, redeploy
   application containers pointed at it, update DNS.
3. If no standby exists: this scenario currently means restoring from the
   most recent off-region backup into a freshly provisioned environment in
   an available region — expect the full RTO window, not the 4-hour
   same-region target.

## Scenario 4: accidental destructive operation

1. Stop write traffic immediately if the operation is still in progress
   (this may mean pausing the `web`/`worker` deployments entirely).
2. Assess blast radius: what tables/rows were affected, and since when?
3. If PostgreSQL's WAL/point-in-time-recovery is available (continuous
   archiving, per the backup runbook), restore to the moment just before
   the destructive operation rather than the last nightly snapshot — this
   is the scenario continuous WAL archiving exists for.
4. If only nightly snapshots exist, accept the data loss back to the last
   snapshot, or attempt manual reconstruction from application logs/audit
   trail for anything with strong secondary evidence (the `audit_log`
   table itself may survive if the destructive operation didn't target
   it, and can help reconstruct what happened).

## After any DR event

Write a postmortem per `docs/runbooks/incident.md`'s "Post-incident"
section, and update this document with anything that didn't work as
planned — a DR runbook that isn't updated after real use tends to be
wrong by the time it's needed again.
