CREATE TABLE IF NOT EXISTS jobs (
    id           bigserial PRIMARY KEY,
    kind         text        NOT NULL,
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status       text        NOT NULL DEFAULT 'queued',
    attempts     integer     NOT NULL DEFAULT 0,
    max_attempts integer     NOT NULL DEFAULT 3,
    run_at       timestamptz NOT NULL DEFAULT now(),
    locked_by    text,
    locked_at    timestamptz,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT jobs_status_check CHECK (status IN ('queued', 'claimed', 'done', 'failed', 'dead')),
    CONSTRAINT jobs_attempts_check CHECK (attempts >= 0),
    CONSTRAINT jobs_max_attempts_check CHECK (max_attempts > 0)
);

CREATE INDEX IF NOT EXISTS idx_jobs_claim ON jobs (run_at, id) WHERE status IN ('queued', 'failed');
CREATE INDEX IF NOT EXISTS idx_jobs_kind ON jobs (kind);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs (status);
