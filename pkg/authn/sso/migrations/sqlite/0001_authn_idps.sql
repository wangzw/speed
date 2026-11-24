-- client_secret is encrypted, NULL for SAML, and reached only through Secret.
-- provisioning has no default, and status 0 is disabled. There is no foreign key, and SQLite stores a time as text in the zone it carries, so the module binds UTC only.
CREATE TABLE IF NOT EXISTS authn_idps (
    id                  TEXT      NOT NULL PRIMARY KEY,
    tenant_id           TEXT      NOT NULL CHECK (tenant_id <> ''),
    protocol            TEXT      NOT NULL CHECK (protocol IN ('oidc', 'saml')),
    name                TEXT      NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    status              SMALLINT  NOT NULL DEFAULT 0 CHECK (status IN (0, 1)),
    provisioning        TEXT      NOT NULL CHECK (provisioning IN ('jit', 'preprovisioned')),
    trust_idp_mfa       BOOLEAN   NOT NULL DEFAULT 0,
    merge_existing      BOOLEAN   NOT NULL DEFAULT 0,
    allow_idp_initiated BOOLEAN   NOT NULL DEFAULT 0,
    allow_sha1          BOOLEAN   NOT NULL DEFAULT 0,
    config              TEXT      NOT NULL CHECK (length(config) <= 65536),
    client_secret       TEXT      NULL,
    version             INTEGER   NOT NULL DEFAULT 1,
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS authn_idps_tenant ON authn_idps (tenant_id);
CREATE TABLE IF NOT EXISTS authn_idp_domains (
    domain       TEXT      NOT NULL,
    idp_id       TEXT      NOT NULL,
    tenant_id    TEXT      NOT NULL CHECK (tenant_id <> ''),
    verify_token TEXT      NOT NULL,
    verified_at  TIMESTAMP NULL,
    created_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (domain, idp_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS authn_idp_domains_verified
    ON authn_idp_domains (domain) WHERE verified_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS authn_saml_assertions (
    idp_id          TEXT      NOT NULL,
    assertion_id    TEXT      NOT NULL CHECK (length(assertion_id) <= 256),
    not_on_or_after TIMESTAMP NOT NULL,
    seen_at         TIMESTAMP NOT NULL,
    PRIMARY KEY (idp_id, assertion_id)
);
CREATE INDEX IF NOT EXISTS authn_saml_assertions_expiry ON authn_saml_assertions (not_on_or_after);
