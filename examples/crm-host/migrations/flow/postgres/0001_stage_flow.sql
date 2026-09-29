-- Each tenant's moves between two stages: how many of its deals moved from
-- one to the other. The flow module's deal.stage-changed subscriber keeps it,
-- inside the tenant the event names.
--
-- tenant_id leads the primary key, as on every scoped table of this host, and
-- the three columns are what the subscriber's upsert conflicts on: a move
-- between two stages the tenant already has a line for adds to that line.
--
-- The stage keys are plain columns rather than a reference to the stages
-- table: the table counts what the events said, and nothing reads it back
-- through the stages.
CREATE TABLE stage_flow (
    tenant_id  TEXT   NOT NULL CHECK (tenant_id <> ''),
    from_stage TEXT   NOT NULL CHECK (from_stage <> ''),
    to_stage   TEXT   NOT NULL CHECK (to_stage <> ''),
    moves      BIGINT NOT NULL CHECK (moves >= 1),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, from_stage, to_stage)
);
