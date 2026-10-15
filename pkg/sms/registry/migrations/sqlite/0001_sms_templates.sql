-- The runtime templates: one row per template this process created or the
-- vendor reviewed. A template declared in configuration has no row here. It is
-- read once at startup and is read-only for the life of the process.
--
-- The name is the primary key, so at most one row carries a template name and a
-- second registration of it conflicts rather than appending a duplicate. The
-- parameter names are a JSON array in the order the approved template expects
-- them, the language-to-code table is a JSON object, and the review table is a
-- JSON object keyed by language, each mapping to that language's verdict, all
-- kept as text because a positional template's order is part of the value and
-- none is queried by its parts.
CREATE TABLE IF NOT EXISTS sms_templates (
    name       TEXT      NOT NULL,
    params     TEXT      NOT NULL,
    codes      TEXT      NOT NULL,
    approvals  TEXT      NOT NULL,
    enabled    INTEGER   NOT NULL DEFAULT 1,
    version    INTEGER   NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (name)
);
