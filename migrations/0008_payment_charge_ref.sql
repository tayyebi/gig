-- Stripe's Checkout Session ID (stored in payment_intents.provider_ref) is
-- not itself refundable; refunds need the underlying PaymentIntent ID,
-- which Stripe only reveals once the session completes. Applied after
-- 0007_seller_payments.sql.

ALTER TABLE payment_intents ADD COLUMN charge_ref text NOT NULL DEFAULT '';
