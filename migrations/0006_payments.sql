-- Fiat payments (Stripe Connect), payment webhooks, refunds, and the
-- double-entry ledger. Applied after 0005_orders.sql.

-- One row per attempt to collect payment for an order via a provider. An
-- order may accumulate more than one intent across retries (e.g. a failed
-- card retried with a fresh Checkout Session), so this is not 1:1 with
-- orders.
CREATE TABLE payment_intents (
    id                 bigserial PRIMARY KEY,
    order_id           bigint      NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    provider           text        NOT NULL,
    provider_ref       text        NOT NULL DEFAULT '',
    method             text        NOT NULL DEFAULT 'card',
    status             text        NOT NULL DEFAULT 'pending',
    amount_minor_units bigint      NOT NULL,
    currency           text        NOT NULL,
    idempotency_key    text        NOT NULL,
    checkout_url       text        NOT NULL DEFAULT '',
    expires_at         timestamptz,
    succeeded_at       timestamptz,
    failed_at          timestamptz,
    canceled_at        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payment_intents_provider_check CHECK (provider IN ('stripe', 'btcpay', 'evm')),
    CONSTRAINT payment_intents_status_check CHECK (status IN
        ('pending', 'processing', 'succeeded', 'failed', 'canceled', 'expired')),
    CONSTRAINT payment_intents_idempotency_key_unique UNIQUE (idempotency_key)
);

CREATE INDEX idx_payment_intents_order ON payment_intents (order_id, created_at DESC);
CREATE INDEX idx_payment_intents_provider_ref ON payment_intents (provider, provider_ref);
CREATE INDEX idx_payment_intents_status ON payment_intents (status);

-- Every raw provider status transition observed for an intent, kept for
-- reconciliation and support even after the intent's own status has moved on.
CREATE TABLE payment_attempts (
    id                bigserial PRIMARY KEY,
    payment_intent_id bigint      NOT NULL REFERENCES payment_intents (id) ON DELETE CASCADE,
    provider_status    text        NOT NULL,
    failure_code      text        NOT NULL DEFAULT '',
    failure_message   text        NOT NULL DEFAULT '',
    raw_payload_hash  text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_payment_attempts_intent ON payment_attempts (payment_intent_id, created_at DESC);

-- Every webhook event, deduplicated by (provider, event_id) so retries and
-- duplicate deliveries can never be double-processed. Processing itself
-- happens asynchronously in a job so the HTTP handler can ack quickly.
CREATE TABLE payment_webhook_events (
    id            bigserial PRIMARY KEY,
    provider      text        NOT NULL,
    event_id      text        NOT NULL,
    event_type    text        NOT NULL,
    payload       jsonb       NOT NULL,
    payload_hash  text        NOT NULL,
    status        text        NOT NULL DEFAULT 'received',
    attempts      integer     NOT NULL DEFAULT 0,
    last_error    text        NOT NULL DEFAULT '',
    processed_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payment_webhook_events_status_check CHECK (status IN ('received', 'processed', 'failed')),
    CONSTRAINT payment_webhook_events_provider_event_unique UNIQUE (provider, event_id)
);

CREATE INDEX idx_payment_webhook_events_status ON payment_webhook_events (status, created_at);

CREATE TABLE refunds (
    id                 bigserial PRIMARY KEY,
    order_id           bigint      NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    payment_intent_id  bigint      NOT NULL REFERENCES payment_intents (id) ON DELETE RESTRICT,
    requested_by       bigint      REFERENCES users (id) ON DELETE SET NULL,
    reason             text        NOT NULL DEFAULT '',
    amount_minor_units bigint      NOT NULL,
    currency           text        NOT NULL,
    status             text        NOT NULL DEFAULT 'pending',
    provider_ref       text        NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT refunds_status_check CHECK (status IN ('pending', 'succeeded', 'failed'))
);

CREATE INDEX idx_refunds_order ON refunds (order_id);
CREATE INDEX idx_refunds_payment_intent ON refunds (payment_intent_id);

-- Double-entry ledger. Every money movement posts at least two balanced
-- entries (see ledger.Validate); entries are append-only, never updated.
CREATE TABLE ledger_accounts (
    id         bigserial PRIMARY KEY,
    kind       text        NOT NULL,
    owner_id   bigint      REFERENCES users (id) ON DELETE RESTRICT,
    currency   text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ledger_accounts_kind_check CHECK (kind IN
        ('platform_revenue', 'seller_pending', 'seller_available', 'refunds', 'reserves', 'provider_clearing')),
    CONSTRAINT ledger_accounts_owner_currency_unique UNIQUE (kind, owner_id, currency)
);

CREATE INDEX idx_ledger_accounts_owner ON ledger_accounts (owner_id) WHERE owner_id IS NOT NULL;

-- transaction_group groups the balanced set of entries produced by one
-- business event (a payment capture, a refund, a payout) so a caller can
-- fetch and verify every entry in that event together.
CREATE TABLE ledger_entries (
    id                 bigserial PRIMARY KEY,
    transaction_group  uuid        NOT NULL,
    account_id         bigint      NOT NULL REFERENCES ledger_accounts (id) ON DELETE RESTRICT,
    direction          text        NOT NULL,
    amount_minor_units bigint      NOT NULL CHECK (amount_minor_units > 0),
    currency           text        NOT NULL,
    order_id           bigint      REFERENCES orders (id) ON DELETE SET NULL,
    description        text        NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ledger_entries_direction_check CHECK (direction IN ('debit', 'credit'))
);

CREATE INDEX idx_ledger_entries_group ON ledger_entries (transaction_group);
CREATE INDEX idx_ledger_entries_account ON ledger_entries (account_id, created_at DESC);
CREATE INDEX idx_ledger_entries_order ON ledger_entries (order_id);
