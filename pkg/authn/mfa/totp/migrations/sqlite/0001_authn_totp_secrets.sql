-- The TOTP seeds, one row per TOTP factor an account has enrolled. The factor
-- register in authn.mfa holds the row that names the account and the kind, and
-- this table holds only the data of this kind, keyed by the factor's identifier.
--
-- secret is an encrypted column. It is written and read through a model, so the
-- database module's serializer seals it on the way in and opens it on the way
-- out, and a handle assembled without a root key fails on the first statement
-- that reaches it rather than storing a column nothing can open. algorithm,
-- digits and period are the parameters the seed was generated with, and a code
-- generated for other ones does not match. last_counter is the greatest counter
-- this factor has accepted, so a code whose counter is not above it is a replay.
--
-- There is no foreign key. Whether the factor exists is the caller's to check,
-- and the row goes with the factor through the account lifecycle hook.
CREATE TABLE IF NOT EXISTS authn_totp_secrets (
    factor_id    TEXT    NOT NULL PRIMARY KEY,
    secret       TEXT    NOT NULL,
    algorithm    TEXT    NOT NULL DEFAULT 'SHA1',
    digits       INTEGER NOT NULL DEFAULT 6,
    period       INTEGER NOT NULL DEFAULT 30,
    last_counter INTEGER NOT NULL DEFAULT 0
);
