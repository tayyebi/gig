# Backup and Restore Runbook

Status: **DRAFT, NOT YET EXERCISED**. This document describes the
procedure; TODO.md's "exercise backup and restore procedures" item
requires actually running this against a real deployment, which needs
production-like infrastructure this repository doesn't provision or run
on its own. Treat this as the procedure to follow the first time you do
that exercise, and update it with anything that turns out to be wrong.

## What needs to be backed up

- **PostgreSQL** (`db` service): the entire system of record — users,
  orders, ledger, sessions, everything. `docker-compose.yml` currently
  bind-mounts data to `./.docker/pgdata` on the host; in production this
  should be a managed database with automated snapshots (e.g. RDS,
  Cloud SQL) rather than a bind-mounted volume on a single host, which has
  no redundancy.
- **Private storage root** (`PrivateStorage`, order deliveries and dispute
  evidence — not in the public `/media/` mount): filesystem, not database.
  Needs its own backup if it isn't already using redundant storage (e.g.
  S3-compatible with versioning) in production.
- **Public media** (`Storage`, portfolio/gig images): lower priority to
  back up strictly — content is seller-supplied and can in principle be
  re-uploaded — but still recommended.
- **Config/secrets** (`.env` in production): back up separately, encrypted,
  outside of application backups (e.g. a secrets manager), never alongside
  a database dump.

## Backup procedure (PostgreSQL)

1. `pg_dump` the `gig` database on a schedule (e.g. nightly full dump plus
   continuous WAL archiving if using a self-managed Postgres instance; a
   managed database service's built-in snapshot feature if not).
2. Store dumps somewhere with independent durability from the primary
   database host (a separate object store, different failure domain).
3. Encrypt backups at rest, since a dump contains every classification
   tier from `docs/data-classification.md`, including Restricted data
   (encrypted wallet address ciphertext — the ciphertext itself isn't
   sensitive without the key, but treat the whole dump as sensitive
   regardless).
4. Retain enough history to recover from "we didn't notice a data problem
   for N days" (recommend at least 30 days of daily backups, longer if
   storage cost allows).

## Restore procedure

1. Provision a fresh PostgreSQL instance (or use a scratch environment,
   never restore directly over a live database without a very good
   reason).
2. Restore the dump (`pg_restore` or the managed service's restore flow).
3. Point a **non-production** instance of this application at the restored
   database (`DATABASE_URL`) and run `store.Migrate` (happens
   automatically at `web` startup) to confirm the schema version matches
   what the application expects — a backup taken before a migration ran
   will need that migration applied on restore, which is expected and
   handled automatically.
4. Spot-check: can you log in as a known test user? Does a known order's
   ledger balance match what was expected at backup time? Do private
   attachment downloads still resolve (this depends on the private storage
   backup being restored to the same location/config)?
5. Only cut production traffic over after this validation passes.

## What "exercising" this means (the still-open TODO item)

Actually run steps 1–5 above against a non-production copy on a schedule
(recommend quarterly, and after any major schema change), not just read
this document. The first time should be treated as a dry run with plenty
of time buffer, since gaps in this procedure will likely surface — expect
to update this document afterward.
