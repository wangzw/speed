-- The two tables of the WebAuthn factor. The library documentation's example
-- schema is PostgreSQL-only (uuidv7(), BYTEA) and fails on SQLite, so the
-- columns here are the types both dialects express. SQLite has no boolean type
-- of its own and stores these under NUMERIC affinity as 0 or 1.
--
-- authn_webauthn_users holds the opaque user handle of an account, one row per
-- account. The handle is a random 32-byte value in base64url and carries
-- nothing of the person, which is what a client-side discoverable credential
-- returns at assertion time for the relying party to find the account by.
--
-- authn_webauthn_credentials holds one row per credential, keyed by the factor
-- row it belongs to. public_key is not a secret. It is kept unencrypted because
-- its integrity matters more than its confidentiality. There is no foreign key,
-- so whether the account exists is the caller's to check, and the rows go with
-- it through the account lifecycle hook. A unique pair of relying party id and
-- credential id stops one authenticator's credential being registered twice.
CREATE TABLE IF NOT EXISTS authn_webauthn_users (
    account_id TEXT NOT NULL PRIMARY KEY,
    handle     TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS authn_webauthn_credentials (
    factor_id        TEXT    NOT NULL PRIMARY KEY,
    rp_id            TEXT    NOT NULL,
    credential_id    TEXT    NOT NULL,
    public_key       TEXT    NOT NULL,
    aaguid           TEXT    NOT NULL,
    attestation_type TEXT    NOT NULL,
    transports       TEXT    NOT NULL DEFAULT '',
    sign_count       INTEGER NOT NULL DEFAULT 0,
    clone_warning    BOOLEAN NOT NULL DEFAULT 0,
    user_verified    BOOLEAN NOT NULL,
    backup_eligible  BOOLEAN NOT NULL,
    backup_state     BOOLEAN NOT NULL,
    passkey          BOOLEAN NOT NULL,
    UNIQUE (rp_id, credential_id)
);
