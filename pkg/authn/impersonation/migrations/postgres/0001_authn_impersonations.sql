-- The impersonation records: one row per impersonation an administrator began.
-- A record is confined to one tenant (tenant_id is NOT NULL and refuses the
-- empty string), carries the reason it was started for and the end of its
-- lifetime, and is never deleted by this module. The retention period is the
-- host's compliance policy, and a query by target is the answer to who
-- impersonated whom.
--
-- The record hangs on the session that started it, the administrator's own. No
-- session is opened for the target. There is no foreign key, because another
-- module's table may be absent and the rows of this table are not the account
-- module's to carry.
--
-- ended_at NULL means the record has not ended. The partial unique index
-- authn_imp_one_active holds one unended record per session, so a session has at
-- most one impersonation in force. A record that has lapsed (its end passed, or
-- its session, actor, target or membership no longer holds) is still unended
-- until a read writes the end back, so the index sees the unended state and not
-- the valid one. A new start on the session ends the lapsed record first.
--
-- Every time is UTC.
CREATE TABLE IF NOT EXISTS authn_impersonations (
    id         TEXT        NOT NULL PRIMARY KEY,
    actor_id   TEXT        NOT NULL,
    target_id  TEXT        NOT NULL,
    tenant_id  TEXT        NOT NULL CHECK (tenant_id <> ''),
    session_id TEXT        NOT NULL,
    reason     TEXT        NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    ends_at    TIMESTAMPTZ NOT NULL,
    ended_at   TIMESTAMPTZ NULL,
    ended_by   TEXT        NULL,
    end_reason TEXT        NULL
);

CREATE INDEX IF NOT EXISTS authn_imp_actor   ON authn_impersonations (actor_id, started_at);
CREATE INDEX IF NOT EXISTS authn_imp_target  ON authn_impersonations (target_id, started_at);
CREATE INDEX IF NOT EXISTS authn_imp_session ON authn_impersonations (session_id);
CREATE UNIQUE INDEX IF NOT EXISTS authn_imp_one_active ON authn_impersonations (session_id) WHERE ended_at IS NULL;
