-- The objects table: one row per object, holding its metadata and carrying it
-- through declaration, upload, completion and deletion. The bytes do not live
-- here; they move through the object store's byte seam, and the key column
-- records where they were put.
--
-- The tenant column leads the key, as it does on every scoped table: the model
-- embeds tenant.Model, and the key the convention declares is the key the table
-- must declare. It is not null, so a row that belongs to no tenant cannot land
-- here.
--
-- Every instant is milliseconds since the Unix epoch rather than a timestamp
-- type. The sweep reads, writes and compares them in native SQL, and the two
-- engines spell a timestamp literal differently while an integer is the same
-- everywhere: an epoch count is what lets one statement judge "expired" against
-- the database's own clock on either engine.
--
-- The declared values a caller gave (declared_*) and the values an upload
-- observed (size, mime, checksum_sha256) are kept apart, so an upload can see
-- that a byte count differs from the declaration without overwriting it.
CREATE TABLE IF NOT EXISTS objects (
    id                TEXT      NOT NULL,
    tenant_id         TEXT      NOT NULL,
    key               TEXT      NOT NULL CHECK (length(key) <= 256),
    state             TEXT      NOT NULL CHECK (state IN ('pending', 'uploaded', 'completed', 'deleted')),
    declared_size     INTEGER   NOT NULL,
    declared_type     TEXT      NOT NULL,
    declared_checksum TEXT      NOT NULL DEFAULT '',
    size              INTEGER   NOT NULL DEFAULT 0,
    mime              TEXT      NOT NULL DEFAULT '',
    checksum_sha256   TEXT      NOT NULL DEFAULT '',
    upload_expires_at INTEGER   NOT NULL,
    expires_at        INTEGER,
    created_at        INTEGER   NOT NULL,
    updated_at        INTEGER   NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

-- The list's keyset cursor pages by (created_at, id) within a tenant, so the
-- index leads with the tenant column and orders by the creation time.
CREATE INDEX IF NOT EXISTS objects_tenant_created_idx ON objects (tenant_id, created_at);

-- The sweep reads the rows whose time is up: a pending object whose upload
-- window has closed, and a completed object past its expiry.
CREATE INDEX IF NOT EXISTS objects_upload_expires_idx ON objects (upload_expires_at);

CREATE INDEX IF NOT EXISTS objects_expires_idx ON objects (expires_at);
