-- Seller wallet addresses (encrypted at rest) and the payout queue,
-- Phase 7. A wallet only becomes payout-eligible after its confirmation
-- token is consumed AND eligible_at has passed (cooling-off period), so a
-- compromised account cannot redirect payouts immediately by adding a new
-- address. Applied after 0008_payment_charge_ref.sql.

CREATE TABLE seller_wallets (
    id                 bigserial PRIMARY KEY,
    user_id            bigint NOT NULL REFERENCES users(id),
    network            text NOT NULL, -- 'base' | 'polygon'
    asset              text NOT NULL, -- 'usdc' | 'usdt'
    address_encrypted  bytea NOT NULL,
    address_fingerprint text NOT NULL, -- sha256 of the plaintext address, for duplicate detection without decrypting
    status             text NOT NULL DEFAULT 'pending', -- pending | confirmed | superseded
    confirmed_at       timestamptz,
    eligible_at        timestamptz, -- confirmed_at + cooldown; payouts check this, not confirmed_at
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_seller_wallets_user ON seller_wallets (user_id, network, asset);
CREATE INDEX idx_seller_wallets_status ON seller_wallets (status);

CREATE TABLE payouts (
    id               bigserial PRIMARY KEY,
    seller_id        bigint NOT NULL REFERENCES users(id),
    wallet_id        bigint NOT NULL REFERENCES seller_wallets(id),
    amount_minor     bigint NOT NULL,
    currency         text NOT NULL,
    network          text NOT NULL,
    asset            text NOT NULL,
    status           text NOT NULL DEFAULT 'queued', -- queued | needs_manual_review | ready_for_manual_execution | completed | canceled
    tx_hash          text NOT NULL DEFAULT '',
    reviewed_by      bigint REFERENCES users(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    executed_at      timestamptz,
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_payouts_seller ON payouts (seller_id);
CREATE INDEX idx_payouts_status ON payouts (status);

-- Emergency pause: a single-row settings table admins can flip to halt all
-- payouts platform-wide without a deploy.
CREATE TABLE platform_settings (
    key   text PRIMARY KEY,
    value text NOT NULL
);

INSERT INTO platform_settings (key, value) VALUES ('payouts_paused', 'false');
