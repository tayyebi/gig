-- Phase 8: admin console support — dispute internal notes, message
-- moderation (hide/unhide), user restriction reason, and a generic
-- platform_settings extension for fee/feature-flag management. Reuses the
-- existing platform_settings key/value table (0009_wallets_payouts.sql)
-- rather than adding a new one.

ALTER TABLE disputes
    ADD COLUMN internal_notes text NOT NULL DEFAULT '';

ALTER TABLE order_messages
    ADD COLUMN hidden    boolean NOT NULL DEFAULT false,
    ADD COLUMN hidden_by bigint REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN hidden_at timestamptz;

ALTER TABLE users
    ADD COLUMN restriction_reason text NOT NULL DEFAULT '';

ALTER TABLE platform_settings
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- Seed a couple of admin-configurable settings alongside the existing
-- payouts_paused row so the settings console has something to list.
INSERT INTO platform_settings (key, value) VALUES
    ('platform_fee_bps', '1000'),
    ('feature_stablecoin_payouts', 'true')
ON CONFLICT (key) DO NOTHING;

-- Speeds up admin payment search by provider reference / charge reference.
CREATE INDEX IF NOT EXISTS idx_payment_intents_charge_ref ON payment_intents (charge_ref) WHERE charge_ref <> '';

-- Records the provider's own reference (e.g. a Stripe Checkout Session ID)
-- alongside each webhook delivery, so the admin order+payment timeline can
-- join events to the payment intent they concern directly instead of
-- parsing the raw provider payload shape.
ALTER TABLE payment_webhook_events
    ADD COLUMN provider_ref text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_payment_webhook_events_provider_ref ON payment_webhook_events (provider, provider_ref) WHERE provider_ref <> '';
