-- The identity providers (IdPs) of tenants. An IdP is data and not a module: it
-- appears at run time, its number is not fixed and it carries a secret, so it
-- lives here rather than in the settings or the configuration. id is a UUIDv7
-- text and appears in the callback path and the service-provider metadata
-- address, so a callback carries the provider and not the tenant, which the
-- validation backend cannot resolve.
--
-- client_secret is an encrypted column. It is written and read through a model,
-- so the database module's serializer seals it on the way in and opens it on the
-- way out, and a handle assembled without a root key fails on the first statement
-- that reaches it rather than storing a column nothing can open. It holds a value
-- for an OIDC provider only and is NULL for a SAML one. The reads this module
-- hands out never select it, and Secret is the one way to it.
--
-- provisioning has no default: a row is never written without an explicit choice
-- of how an account is created for a user who signs in through this IdP.
-- merge_existing, allow_idp_initiated and allow_sha1 default to 0 (false).
-- status is 0 (disabled) by default, so a row constructed without being enabled
-- reads as disabled. version rises on every write.
--
-- tenant_id is a bare string: the table embeds no tenant model, because the
-- domain discovery and the callback route have no tenant context. There is no
-- foreign key. Every time is UTC: SQLite compares a time as text in the zone it
-- carries, so the module writes and binds nothing but UTC.
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

-- The email domains of the IdPs, with the ownership proof behind each. A domain
-- is the lower-case ASCII (punycode) form and is globally unique once verified,
-- which is what stops a tenant's administrator from claiming a domain another
-- tenant's IdP has already proven. Unverified claims may stand side by side, so a
-- squatting claim takes no place, and the sweep reclaims the ones left unverified.
-- verify_token is the random value the tenant publishes in the DNS TXT record
-- _speed-authn.<domain> as speed-authn=<token>.
CREATE TABLE IF NOT EXISTS authn_idp_domains (
    domain       TEXT      NOT NULL,
    idp_id       TEXT      NOT NULL,
    tenant_id    TEXT      NOT NULL CHECK (tenant_id <> ''),
    verify_token TEXT      NOT NULL,
    verified_at  TIMESTAMP NULL,
    created_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (domain, idp_id)
);

-- A verified domain is globally unique. The partial index is what a concurrent
-- second verification of the same domain loses to (ErrDomainClaimed).
CREATE UNIQUE INDEX IF NOT EXISTS authn_idp_domains_verified
    ON authn_idp_domains (domain) WHERE verified_at IS NOT NULL;

-- The identifiers of the SAML assertions already consumed. The primary key is the
-- replay line: a second consumption of one assertion collides with it. It is a
-- table and not a key-value store because a lost key there would reopen the
-- replay window. not_on_or_after is the earlier of the assertion's and its
-- subject confirmation's condition, and the sweep removes rows past it.
CREATE TABLE IF NOT EXISTS authn_saml_assertions (
    idp_id          TEXT      NOT NULL,
    assertion_id    TEXT      NOT NULL CHECK (length(assertion_id) <= 256),
    not_on_or_after TIMESTAMP NOT NULL,
    seen_at         TIMESTAMP NOT NULL,
    PRIMARY KEY (idp_id, assertion_id)
);

CREATE INDEX IF NOT EXISTS authn_saml_assertions_expiry ON authn_saml_assertions (not_on_or_after);
