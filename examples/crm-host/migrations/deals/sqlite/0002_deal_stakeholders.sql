-- The stakeholders of each tenant's deals: one row per deal and contact the
-- deal names.
--
-- The table is the join table of the deal's many-to-many association with the
-- contacts. The association writes the tenant half of both keys into
-- tenant_id, which is how the tenant filter takes it for the table's tenant
-- column and scopes the table as it scopes the two tables it joins. The
-- tenant column leads the primary key and is the one both references share:
-- a deal names a contact of its own tenant, and the database refuses any other
-- pairing as well as the look-up the deals module makes first. The reference
-- to the contacts table needs the contacts migration applied first, and the
-- deals module declares a requirement on the contacts module's table token for
-- that alone.
--
-- A row is removed when the link is: the association deletes it. A deal's or a
-- contact's delete is soft, so a link to one stays, and a read leaves the
-- deleted side out.
CREATE TABLE deal_stakeholders (
    tenant_id  TEXT NOT NULL CHECK (tenant_id <> ''),
    deal_id    TEXT NOT NULL,
    contact_id TEXT NOT NULL,
    PRIMARY KEY (tenant_id, deal_id, contact_id),
    FOREIGN KEY (tenant_id, deal_id) REFERENCES deals (tenant_id, id),
    FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id)
);

-- Removing a contact's row checks the reference by the contact, so the second
-- index leads with the tenant column and the contact.
CREATE INDEX deal_stakeholders_contact_idx ON deal_stakeholders (tenant_id, contact_id);
