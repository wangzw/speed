-- The events transactions staged for the bus: a row is written in the transaction that makes the write it announces, and removed once the bus accepts the event.
-- Times are milliseconds since the Unix epoch. AUTOINCREMENT keeps identifiers from being reused, so they keep the staging order.
CREATE TABLE event_staged (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id     TEXT    NOT NULL,
    type         TEXT    NOT NULL,
    tenant_id    TEXT    NOT NULL,
    payload      TEXT    NOT NULL,
    staged_at    INTEGER NOT NULL,
    available_at INTEGER NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 0,
    claimed_by   TEXT,
    last_error   TEXT
);

CREATE INDEX event_staged_available_at ON event_staged (available_at);
