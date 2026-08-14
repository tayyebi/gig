-- Orders, fulfillment, messaging, disputes, reviews, and notifications.
-- Applied after 0004_catalog.sql.

-- Multi-step checkout progress, one draft per buyer per gig so a refresh
-- never loses the step (PLAN.md section 11).
CREATE TABLE draft_orders (
    id           bigserial PRIMARY KEY,
    buyer_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    gig_id       bigint      NOT NULL REFERENCES gigs (id) ON DELETE CASCADE,
    package_id   bigint      NOT NULL REFERENCES gig_packages (id) ON DELETE CASCADE,
    requirements text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_draft_orders_buyer_gig ON draft_orders (buyer_id, gig_id);

CREATE TABLE draft_order_addons (
    draft_order_id bigint NOT NULL REFERENCES draft_orders (id) ON DELETE CASCADE,
    addon_id       bigint NOT NULL REFERENCES gig_addons (id) ON DELETE CASCADE,
    PRIMARY KEY (draft_order_id, addon_id)
);

-- Orders. status is the single source of truth for the order state machine
-- (enforced in services.CanTransitionOrder / store.TransitionOrder); nothing
-- about the allowed transitions is inferred from client input.
CREATE TABLE orders (
    id                        bigserial PRIMARY KEY,
    buyer_id                  bigint      NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    seller_id                 bigint      NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    gig_id                    bigint      NOT NULL REFERENCES gigs (id) ON DELETE RESTRICT,
    status                    text        NOT NULL DEFAULT 'pending_payment',
    requirements              text        NOT NULL DEFAULT '',
    revisions_used            integer     NOT NULL DEFAULT 0,
    revisions_allowed         integer     NOT NULL DEFAULT 0,
    subtotal_minor_units      bigint      NOT NULL,
    platform_fee_minor_units  bigint      NOT NULL DEFAULT 0,
    total_minor_units         bigint      NOT NULL,
    currency                  text        NOT NULL DEFAULT 'USD',
    paid_at                   timestamptz,
    delivered_at              timestamptz,
    accepted_at               timestamptz,
    cancelled_at              timestamptz,
    closed_at                 timestamptz,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT orders_status_check CHECK (status IN (
        'pending_payment', 'payment_failed', 'paid', 'in_progress', 'delivered',
        'revision_requested', 'accepted', 'disputed', 'cancel_requested', 'cancelled',
        'payout_pending', 'paid_out', 'closed'))
);

CREATE INDEX idx_orders_buyer ON orders (buyer_id, created_at DESC);
CREATE INDEX idx_orders_seller ON orders (seller_id, created_at DESC);
CREATE INDEX idx_orders_status ON orders (status);

-- Immutable snapshot of what was purchased. Never updated after insert, so
-- later gig/package edits cannot rewrite financial history.
CREATE TABLE order_items (
    id                bigserial PRIMARY KEY,
    order_id          bigint      NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    kind              text        NOT NULL,
    name              text        NOT NULL,
    description       text        NOT NULL DEFAULT '',
    price_minor_units bigint      NOT NULL,
    currency          text        NOT NULL DEFAULT 'USD',
    delivery_days     integer,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT order_items_kind_check CHECK (kind IN ('package', 'addon'))
);

CREATE INDEX idx_order_items_order ON order_items (order_id);

CREATE TABLE order_messages (
    id         bigserial PRIMARY KEY,
    order_id   bigint      NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    sender_id  bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    body       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_order_messages_order ON order_messages (order_id, created_at);

-- Attachments are never served from the public /media/ path: the serving
-- handler checks the caller is the order's buyer, seller, or an admin. That
-- is this session-based app's equivalent of PLAN.md's "signed URLs" for
-- private attachments; there is no bearer-token CDN layer to sign for.
CREATE TABLE order_attachments (
    id          bigserial PRIMARY KEY,
    order_id    bigint      NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    uploader_id bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        text        NOT NULL DEFAULT 'message',
    file_path   text        NOT NULL,
    file_name   text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT order_attachments_kind_check CHECK (kind IN ('delivery', 'message', 'dispute_evidence'))
);

CREATE INDEX idx_order_attachments_order ON order_attachments (order_id);

CREATE TABLE deliveries (
    id         bigserial PRIMARY KEY,
    order_id   bigint      NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    seller_id  bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    version    integer     NOT NULL,
    message    text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deliveries_order_version_unique UNIQUE (order_id, version)
);

CREATE INDEX idx_deliveries_order ON deliveries (order_id);

CREATE TABLE revision_requests (
    id         bigserial PRIMARY KEY,
    order_id   bigint      NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    buyer_id   bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason     text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_revision_requests_order ON revision_requests (order_id);

CREATE TABLE cancellation_requests (
    id           bigserial PRIMARY KEY,
    order_id     bigint      NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    requested_by bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason       text        NOT NULL,
    status       text        NOT NULL DEFAULT 'pending',
    resolved_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT cancellation_requests_status_check CHECK (status IN ('pending', 'approved', 'declined'))
);

CREATE INDEX idx_cancellation_requests_order ON cancellation_requests (order_id);

CREATE TABLE disputes (
    id          bigserial PRIMARY KEY,
    order_id    bigint      NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    opened_by   bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason      text        NOT NULL,
    status      text        NOT NULL DEFAULT 'open',
    decision    text        NOT NULL DEFAULT '',
    resolved_by bigint REFERENCES users (id) ON DELETE SET NULL,
    resolved_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT disputes_status_check CHECK (status IN ('open', 'resolved'))
);

CREATE INDEX idx_disputes_order ON disputes (order_id);
CREATE INDEX idx_disputes_status ON disputes (status);

CREATE TABLE reviews (
    id               bigserial PRIMARY KEY,
    order_id         bigint      NOT NULL UNIQUE REFERENCES orders (id) ON DELETE CASCADE,
    gig_id           bigint      NOT NULL REFERENCES gigs (id) ON DELETE CASCADE,
    buyer_id         bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    seller_id        bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rating           integer     NOT NULL,
    body             text        NOT NULL DEFAULT '',
    moderation_state text        NOT NULL DEFAULT 'approved',
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reviews_rating_check CHECK (rating BETWEEN 1 AND 5),
    CONSTRAINT reviews_moderation_check CHECK (moderation_state IN ('pending', 'approved', 'rejected'))
);

CREATE INDEX idx_reviews_gig ON reviews (gig_id);
CREATE INDEX idx_reviews_seller ON reviews (seller_id);

CREATE TABLE notifications (
    id         bigserial PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       text        NOT NULL,
    body       text        NOT NULL,
    link       text        NOT NULL DEFAULT '',
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_notifications_user ON notifications (user_id, created_at DESC);
CREATE INDEX idx_notifications_unread ON notifications (user_id) WHERE read_at IS NULL;
