CREATE TABLE users (
    id                bigserial PRIMARY KEY,
    email             text        NOT NULL,
    email_lower       text        NOT NULL UNIQUE,
    password_hash     text        NOT NULL,
    name              text        NOT NULL,
    locale            text        NOT NULL DEFAULT 'en',
    status            text        NOT NULL DEFAULT 'active',
    email_verified_at timestamptz,
    totp_secret       text,
    totp_enabled_at   timestamptz,
    last_login_at     timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_status_check CHECK (status IN ('active', 'disabled', 'deleted')),
    CONSTRAINT users_email_format CHECK (email ~* '^[^@\s]+@[^@\s]+\.[^@\s]+$'),
    CONSTRAINT users_totp_secret_format CHECK (totp_secret IS NULL OR length(totp_secret) >= 16)
);

CREATE INDEX idx_users_email ON users (email);

CREATE TABLE user_roles (
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role),
    CONSTRAINT user_roles_role_check CHECK (role IN ('buyer', 'seller', 'admin'))
);

CREATE INDEX idx_user_roles_role ON user_roles (role);

CREATE TABLE sessions (
    id          bigserial PRIMARY KEY,
    user_id     bigint REFERENCES users (id) ON DELETE CASCADE,
    token_hash  text        NOT NULL UNIQUE,
    csrf_token  text        NOT NULL,
    expires_at  timestamptz NOT NULL,
    user_agent  text        NOT NULL DEFAULT '',
    ip          text        NOT NULL DEFAULT '',
    flash       jsonb       NOT NULL DEFAULT 'null'::jsonb,
    revoked_at  timestamptz,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sessions_token_hash_format CHECK (length(token_hash) = 64),
    CONSTRAINT sessions_csrf_format CHECK (length(csrf_token) >= 16)
);

CREATE INDEX idx_sessions_user ON sessions (user_id);
CREATE INDEX idx_sessions_expiry ON sessions (expires_at) WHERE revoked_at IS NULL;

CREATE TABLE auth_tokens (
    id         bigserial PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       text        NOT NULL,
    token_hash text        NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT auth_tokens_kind_check CHECK (kind IN ('email_verification', 'password_reset')),
    CONSTRAINT auth_tokens_hash_format CHECK (length(token_hash) = 64)
);

CREATE INDEX idx_auth_tokens_user ON auth_tokens (user_id, kind);

CREATE TABLE audit_log (
    id            bigserial PRIMARY KEY,
    actor_user_id bigint REFERENCES users (id) ON DELETE SET NULL,
    actor_ip      text        NOT NULL DEFAULT '',
    action        text        NOT NULL,
    entity_type   text,
    entity_id     text,
    details       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_actor ON audit_log (actor_user_id, created_at);
CREATE INDEX idx_audit_log_action ON audit_log (action, created_at);
