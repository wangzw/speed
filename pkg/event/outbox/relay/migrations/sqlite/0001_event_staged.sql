-- The events transactions staged for the bus. A row is written in the
-- transaction that makes the write it announces, and removed once the bus has
-- accepted the event it carries.
--
-- Times are milliseconds since the Unix epoch. AUTOINCREMENT keeps an
-- identifier from being reused once the row holding the largest one is removed,
-- so the identifiers keep the order the rows were staged in.
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

-- A relay looks for the rows whose time has come.
CREATE INDEX event_staged_available_at ON event_staged (available_at);
