# Gig Marketplace

A full-platform, mobile-first gig marketplace with fiat and crypto payments.
Zero JavaScript in the front end, pure-Go backend (standard library HTTP,
templating, and routing), and PostgreSQL.

## Stack

- Go 1.22+ (uses Go 1.22 `net/http` method-and-path routing)
- PostgreSQL 17 via `database/sql` with the pgx driver
- `html/template` component-style server-side rendering
- Docker Compose: `db`, `web`, and `worker` on an internal network
- Embedded SQL migrations applied at startup under an advisory lock

## Run with Docker Compose

```sh
docker compose up -d --build
```

- Web: http://localhost:4099
- Health: http://localhost:4099/healthz, http://localhost:4099/readyz
- PostgreSQL is not published to the host; reach it inside the compose
  network as `db:5432`, or via `docker compose exec db psql -U gig gig`.

`web` applies migrations at startup and serves HTTP; `worker` runs the same
binary with `APP_ROLE=worker` and consumes the PostgreSQL-backed job queue.

## Run locally

```sh
cp .env.example .env      # then edit DATABASE_URL etc.
export $(cat .env | xargs)
go run .
```

## Development

```sh
make test                 # unit tests (no database required)
make up                   # build and start compose services
make test-integration     # store integration tests against the compose db
make ci                   # fmt check + vet + build + test
```

## Project layout

Flat repository: root-level Go files plus one level of subdirectories.

- `components/` — HTML components rendered from `html/template` fragments
- `handlers/` — thin HTTP handlers grouped by concern
- `services/` — domain logic (business rules, no HTTP)
- `store/` — PostgreSQL data access and the job queue
- `providers/` — payment adapters (Stripe, BTCPay, EVM, wallets)
- `ledger/` — double-entry accounting and reconciliation
- `migrations/` — embedded SQL files applied at startup
- `static/` — the single authored stylesheet and assets
- `config/` — environment parsing and validation

See `PLAN.md` for the full architecture and `TODO.md` for the implementation
checklist.

## Security constraints

- No `<script>` elements, inline handlers, or third-party widgets are ever
  rendered.
- CSP is `script-src 'none'`; sessions are PostgreSQL-backed and HttpOnly;
  all money-changing requests require idempotency keys.
