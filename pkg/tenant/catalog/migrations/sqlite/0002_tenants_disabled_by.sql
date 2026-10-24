-- The source of a disable: zero is the operator's, and also the value of a
-- source never recorded; one is account state's. An existing row takes the
-- column default, which is the operator's, so a disable already in place is one
-- account state must not revoke.
ALTER TABLE tenants ADD COLUMN disabled_by INTEGER NOT NULL DEFAULT 0;
