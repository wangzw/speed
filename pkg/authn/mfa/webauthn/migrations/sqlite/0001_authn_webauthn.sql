-- The handle is a random 32-byte base64url value carrying nothing of the person.
-- public_key is unencrypted because integrity matters more than confidentiality. UNIQUE (rp_id, credential_id) stops a credential being registered twice.
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
