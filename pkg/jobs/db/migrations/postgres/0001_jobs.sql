-- The job table: one row per job handed to the background.
--
-- The tenant column leads the key, as it does on every scoped table: the model
-- carries tenant.Model, and the key the convention declares is the key the
-- table must declare. It is not null, so a row that belongs to no tenant cannot
-- land here.
--
-- Every instant is stored as milliseconds since the Unix epoch rather than as a
-- timestamp type. The claim loop reads, writes and compares them in native SQL,
-- and the two engines spell a timestamp literal differently while an integer is
-- the same everywhere: an epoch count is what lets one statement judge "due
-- now" and "claimed until" against the database's own clock on either engine.
--
-- available_at is the instant a row may next be claimed; the claim reads the
-- table by it, and an index on it is what keeps that read from scanning. The
-- unique index on (tenant_id, idempotency_key) is what makes a repeated enqueue
-- with an idempotency key converge on the row already there; a row that carries
-- no key writes null, and an engine judges two nulls distinct, so keyless jobs
-- coexist.
CREATE TABLE IF NOT EXISTS jobs (
    id                    TEXT        NOT NULL,
    tenant_id             TEXT        NOT NULL,
    type                  TEXT        NOT NULL,
    status                TEXT        NOT NULL,
    priority              BIGINT      NOT NULL,
    payload               BYTEA,
    result                BYTEA,
    error                 TEXT,
    attempts              BIGINT      NOT NULL,
    max_retries           BIGINT      NOT NULL,
    timeout_ms            BIGINT      NOT NULL,
    progress_pct          BIGINT      NOT NULL,
    progress_msg          TEXT        NOT NULL,
    idempotency_key       TEXT,
    scheduled_at          BIGINT      NOT NULL,
    available_at          BIGINT      NOT NULL,
    started_at            BIGINT,
    completed_at          BIGINT,
    claimed_by            TEXT,
    claim_expires_at      BIGINT,
    cancel_requested      BOOLEAN     NOT NULL,
    terminal_published_at BIGINT,
    created_at            BIGINT      NOT NULL,
    updated_at            BIGINT      NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS jobs_available_at_idx ON jobs (available_at);

CREATE UNIQUE INDEX IF NOT EXISTS jobs_tenant_idempotency_key_idx ON jobs (tenant_id, idempotency_key);
