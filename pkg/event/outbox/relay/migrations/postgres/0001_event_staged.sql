-- The events transactions staged for the bus: a row is written in the transaction that makes the write it announces, and removed once the bus accepts the event.
-- Times are milliseconds since the Unix epoch on the database's clock, so every replica's relay compares its claims against one clock.
CREATE TABLE event_staged (
    id           BIGSERIAL PRIMARY KEY,
    event_id     TEXT      NOT NULL,
    type         TEXT      NOT NULL,
    tenant_id    TEXT      NOT NULL,
    payload      TEXT      NOT NULL,
    staged_at    BIGINT    NOT NULL,
    available_at BIGINT    NOT NULL,
    attempts     INTEGER   NOT NULL DEFAULT 0,
    claimed_by   TEXT,
    last_error   TEXT
);

CREATE INDEX event_staged_available_at ON event_staged (available_at);
