-- The account store: accounts, the identifiers they are found by, their
-- external identities, their tenant memberships, their password credentials,
-- the secrets of service accounts and the failure back-off.
--
-- No table has a foreign key and none embeds the tenant model. A column that
-- names an account is only a column, and the writer of a row keeps it whole in
-- its own transaction. Every time is stored in UTC.

CREATE TABLE IF NOT EXISTS authn_accounts (
    id            TEXT        NOT NULL PRIMARY KEY,
    kind          TEXT        NOT NULL CHECK (kind IN ('user', 'service')),
    status        SMALLINT    NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2)),
    display_name  TEXT        NOT NULL DEFAULT '' CHECK (length(display_name) <= 100),
    attributes    TEXT        NOT NULL DEFAULT '{}',
    mfa_required  BOOLEAN     NOT NULL DEFAULT FALSE,
    protected     BOOLEAN     NOT NULL DEFAULT FALSE,
    valid_after   TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    last_login_at TIMESTAMPTZ NULL,
    disabled_at   TIMESTAMPTZ NULL,
    deleted_at    TIMESTAMPTZ NULL
);

-- value is compared bytewise, so that the range a prefix listing reads is the
-- same range on every collation the database may have been created with.
CREATE TABLE IF NOT EXISTS authn_identifiers (
    id           TEXT        NOT NULL PRIMARY KEY,
    account_id   TEXT        NOT NULL,
    kind         TEXT        NOT NULL CHECK (kind IN ('username', 'email', 'phone')),
    value        TEXT COLLATE "C" NOT NULL,
    verified_at  TIMESTAMPTZ NOT NULL,
    verified_via TEXT        NOT NULL CHECK (verified_via <> ''),
    created_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (kind, value)
);

CREATE INDEX IF NOT EXISTS authn_identifiers_account ON authn_identifiers (account_id);

CREATE UNIQUE INDEX IF NOT EXISTS authn_identifiers_one_phone ON authn_identifiers (account_id) WHERE kind = 'phone';

CREATE UNIQUE INDEX IF NOT EXISTS authn_identifiers_one_username ON authn_identifiers (account_id) WHERE kind = 'username';

CREATE TABLE IF NOT EXISTS authn_external_identities (
    id             TEXT        NOT NULL PRIMARY KEY,
    account_id     TEXT        NOT NULL,
    provider       TEXT        NOT NULL,
    subject        TEXT        NOT NULL,
    email          TEXT        NOT NULL DEFAULT '',
    email_verified BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL,
    last_login_at  TIMESTAMPTZ NULL,
    UNIQUE (provider, subject)
);

CREATE INDEX IF NOT EXISTS authn_external_identities_account ON authn_external_identities (account_id);

CREATE TABLE IF NOT EXISTS authn_memberships (
    tenant_id  TEXT        NOT NULL CHECK (tenant_id <> ''),
    account_id TEXT        NOT NULL,
    status     SMALLINT    NOT NULL DEFAULT 0 CHECK (status IN (0, 1)),
    source     TEXT        NOT NULL DEFAULT '',
    sso_exempt BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, account_id)
);

CREATE INDEX IF NOT EXISTS authn_memberships_account ON authn_memberships (account_id, tenant_id);

CREATE TABLE IF NOT EXISTS authn_password_credentials (
    account_id  TEXT        NOT NULL PRIMARY KEY,
    phc         TEXT        NOT NULL,
    changed_at  TIMESTAMPTZ NOT NULL,
    must_change BOOLEAN     NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS authn_service_secrets (
    id           TEXT        NOT NULL PRIMARY KEY,
    account_id   TEXT        NOT NULL,
    secret_hash  TEXT        NOT NULL UNIQUE,
    label        TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NULL,
    revoked_at   TIMESTAMPTZ NULL,
    last_used_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS authn_service_secrets_account ON authn_service_secrets (account_id);

CREATE TABLE IF NOT EXISTS authn_throttles (
    scope            TEXT        NOT NULL,
    key_hash         TEXT        NOT NULL,
    failures         INTEGER     NOT NULL,
    first_failure_at TIMESTAMPTZ NOT NULL,
    last_failure_at  TIMESTAMPTZ NOT NULL,
    locked_until     TIMESTAMPTZ NULL,
    PRIMARY KEY (scope, key_hash)
);

CREATE INDEX IF NOT EXISTS authn_throttles_decay ON authn_throttles (last_failure_at);
