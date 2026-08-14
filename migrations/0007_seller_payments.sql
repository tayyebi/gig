-- Stripe Connect account linkage for seller payouts, and refund tracking
-- fields the refund flow needs. Applied after 0006_payments.sql.

ALTER TABLE seller_onboarding
    ADD COLUMN stripe_account_id text NOT NULL DEFAULT '',
    ADD COLUMN charges_enabled   boolean NOT NULL DEFAULT false,
    ADD COLUMN payouts_enabled   boolean NOT NULL DEFAULT false;

CREATE UNIQUE INDEX idx_seller_onboarding_stripe_account
    ON seller_onboarding (stripe_account_id) WHERE stripe_account_id <> '';
