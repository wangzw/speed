-- Each tenant's won deals, per currency: how many, and their amounts added up.
-- The revenue module's deal.won subscriber keeps it, inside the tenant the
-- event names.
--
-- tenant_id leads the primary key, as on every scoped table of this host, and
-- the pair is what the subscriber's upsert conflicts on: a won deal in a
-- currency the tenant already has a line for adds to that line.
--
-- amount is a 64-bit integer of minor units, so a sum stays exact for as long
-- as it fits one; the CHECK refuses a sum that went negative.
CREATE TABLE won_revenue (
    tenant_id  TEXT   NOT NULL CHECK (tenant_id <> ''),
    currency   TEXT   NOT NULL CHECK (currency <> ''),
    deals      BIGINT NOT NULL CHECK (deals >= 1),
    amount     BIGINT NOT NULL CHECK (amount >= 0),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, currency)
);
