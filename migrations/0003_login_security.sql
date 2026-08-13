-- Brute-force protection: track failed logins per account and a lockout
-- window. Applied after 0002_identity.sql.
ALTER TABLE users
    ADD COLUMN failed_login_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN locked_until        timestamptz;
