-- Marketplace catalog: seller profiles, onboarding, portfolio, gigs,
-- packages, add-ons, media, and favorites. Applied after 0003_login_security.sql.

CREATE TABLE seller_profiles (
    user_id                bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    display_name           text        NOT NULL,
    bio                    text        NOT NULL DEFAULT '',
    location               text        NOT NULL DEFAULT '',
    response_time_minutes  integer,
    rating_avg             numeric(3, 2) NOT NULL DEFAULT 0,
    rating_count           integer     NOT NULL DEFAULT 0,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE seller_onboarding (
    user_id      bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    kyc_state    text        NOT NULL DEFAULT 'not_started',
    requirements jsonb       NOT NULL DEFAULT '{}'::jsonb,
    reviewed_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT seller_onboarding_kyc_state_check
        CHECK (kyc_state IN ('not_started', 'pending', 'verified', 'rejected'))
);

CREATE TABLE portfolio_items (
    id               bigserial PRIMARY KEY,
    user_id          bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title            text        NOT NULL,
    description      text        NOT NULL DEFAULT '',
    media_path       text        NOT NULL,
    moderation_state text        NOT NULL DEFAULT 'approved',
    position         integer     NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT portfolio_items_moderation_check
        CHECK (moderation_state IN ('pending', 'approved', 'rejected'))
);

CREATE INDEX idx_portfolio_items_user ON portfolio_items (user_id, position);

CREATE TABLE categories (
    id          bigserial PRIMARY KEY,
    slug        text        NOT NULL UNIQUE,
    name        text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    position    integer     NOT NULL DEFAULT 0
);

CREATE TABLE tags (
    id   bigserial PRIMARY KEY,
    slug text      NOT NULL UNIQUE,
    name text      NOT NULL
);

CREATE TABLE gigs (
    id               bigserial PRIMARY KEY,
    seller_id        bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    category_id      bigint      REFERENCES categories (id) ON DELETE SET NULL,
    slug             text        NOT NULL UNIQUE,
    title            text        NOT NULL,
    description      text        NOT NULL DEFAULT '',
    status           text        NOT NULL DEFAULT 'draft',
    moderation_state text        NOT NULL DEFAULT 'approved',
    search_vector    tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(description, '')), 'B')
    ) STORED,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT gigs_status_check CHECK (status IN ('draft', 'active', 'paused', 'archived')),
    CONSTRAINT gigs_moderation_check CHECK (moderation_state IN ('pending', 'approved', 'rejected'))
);

CREATE INDEX idx_gigs_seller ON gigs (seller_id);
CREATE INDEX idx_gigs_category ON gigs (category_id);
CREATE INDEX idx_gigs_listing ON gigs (status, moderation_state, created_at DESC);
CREATE INDEX idx_gigs_search ON gigs USING GIN (search_vector);

CREATE TABLE gig_tags (
    gig_id bigint NOT NULL REFERENCES gigs (id) ON DELETE CASCADE,
    tag_id bigint NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (gig_id, tag_id)
);

CREATE INDEX idx_gig_tags_tag ON gig_tags (tag_id);

CREATE TABLE gig_packages (
    id                bigserial PRIMARY KEY,
    gig_id            bigint      NOT NULL REFERENCES gigs (id) ON DELETE CASCADE,
    tier              text        NOT NULL,
    name              text        NOT NULL,
    description       text        NOT NULL DEFAULT '',
    price_minor_units bigint      NOT NULL,
    currency          text        NOT NULL DEFAULT 'USD',
    delivery_days     integer     NOT NULL,
    revisions         integer     NOT NULL DEFAULT 0,
    CONSTRAINT gig_packages_tier_check CHECK (tier IN ('basic', 'standard', 'premium')),
    CONSTRAINT gig_packages_price_check CHECK (price_minor_units > 0),
    CONSTRAINT gig_packages_delivery_check CHECK (delivery_days > 0),
    CONSTRAINT gig_packages_gig_tier_unique UNIQUE (gig_id, tier)
);

CREATE TABLE gig_addons (
    id                   bigserial PRIMARY KEY,
    gig_id               bigint      NOT NULL REFERENCES gigs (id) ON DELETE CASCADE,
    name                 text        NOT NULL,
    description          text        NOT NULL DEFAULT '',
    price_minor_units    bigint      NOT NULL,
    currency             text        NOT NULL DEFAULT 'USD',
    delivery_days_impact integer     NOT NULL DEFAULT 0,
    position             integer     NOT NULL DEFAULT 0,
    CONSTRAINT gig_addons_price_check CHECK (price_minor_units > 0)
);

CREATE INDEX idx_gig_addons_gig ON gig_addons (gig_id, position);

CREATE TABLE gig_media (
    id               bigserial PRIMARY KEY,
    gig_id           bigint      NOT NULL REFERENCES gigs (id) ON DELETE CASCADE,
    media_path       text        NOT NULL,
    alt_text         text        NOT NULL DEFAULT '',
    position         integer     NOT NULL DEFAULT 0,
    moderation_state text        NOT NULL DEFAULT 'approved',
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT gig_media_moderation_check CHECK (moderation_state IN ('pending', 'approved', 'rejected'))
);

CREATE INDEX idx_gig_media_gig ON gig_media (gig_id, position);

CREATE TABLE favorites (
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    gig_id     bigint      NOT NULL REFERENCES gigs (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, gig_id)
);

CREATE INDEX idx_favorites_gig ON favorites (gig_id);

-- Starter categories so browse and search have content to organize under.
-- Full category/tag management is an admin (Phase 8) responsibility; this
-- seed unblocks catalog features until that console exists.
INSERT INTO categories (slug, name, description, position) VALUES
    ('design', 'Design', 'Logos, brand kits, and more', 1),
    ('development', 'Development', 'Websites, scripts, and integrations', 2),
    ('writing', 'Writing', 'Copy, editing, and translation', 3),
    ('marketing', 'Marketing', 'Social media, SEO, and ads', 4);
