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

- Web: https://localhost:4100
- Health: https://localhost:4100/healthz, https://localhost:4100/readyz

`nginx` publishes the only host port (4100) and terminates TLS; `web`, `worker`,
and `db` are reachable only on the internal compose network. For host-side
database access use `docker compose exec db psql -U gig gig`.

`web` applies migrations at startup and serves HTTP; `worker` runs the same
binary with `APP_ROLE=worker` and consumes the PostgreSQL-backed job queue.

### TLS certificate

On first start nginx generates a self-signed certificate into `.docker/certs/`
and reuses it thereafter, so browsers only need to be told to trust it once.
Browsers will still warn — that is expected for a self-signed certificate.

The certificate is issued for `localhost` by default. When serving from a real
server, set the names it should cover *before* the first start:

```sh
CERT_HOSTS=gig.example.com,203.0.113.10 docker compose up -d --build
```

To reissue (for example after changing `CERT_HOSTS`), delete `.docker/certs/`
and restart the `nginx` service.

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
