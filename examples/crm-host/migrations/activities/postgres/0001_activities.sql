-- What happened to each tenant's deals: notes, calls, emails, meetings, and
-- the moves between stages.
--
-- tenant_id leads the primary key, as on every scoped table of this host.
--
-- The subject is a kind and an identifier. A deal is the one kind so far, and
-- the composite reference holds the identifier to a deal of the same tenant;
-- the activities module declares a requirement on the deals module's table
-- token so that this migration is applied after that one.
--
-- event_id and won_event_id are the identifiers of the events a stage move
-- published, written in the move's own transaction. Each identifies one event,
-- so neither repeats within a tenant.
CREATE TABLE activities (
    tenant_id    TEXT NOT NULL CHECK (tenant_id <> ''),
    id           TEXT NOT NULL CHECK (id <> ''),
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('deal')),
    subject_id   TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('note', 'call', 'email', 'meeting', 'stage-change')),
    body         TEXT NOT NULL,
    actor_id     TEXT NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL,
    event_id     TEXT,
    won_event_id TEXT,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, subject_id) REFERENCES deals (tenant_id, id)
);

CREATE INDEX activities_subject_idx ON activities (tenant_id, subject_kind, subject_id, occurred_at);
CREATE UNIQUE INDEX activities_event_idx ON activities (tenant_id, event_id);
CREATE UNIQUE INDEX activities_won_event_idx ON activities (tenant_id, won_event_id);
