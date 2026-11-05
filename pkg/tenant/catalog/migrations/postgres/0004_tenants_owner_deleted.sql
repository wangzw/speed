-- The organization's owner and its deletion tombstone.
--
-- owner_account names the account that owns the organization. It is NOT NULL
-- with the empty string as its unset value: a business organization's creation
-- or transfer seam writes a real account, a personal organization's owner is the
-- account itself, and the system tenant carries none. The roster refuses a stored
-- value that is not an identifier, and the empty value is the only one it reads
-- as "no owner yet".
--
-- deleted_at is the moment a tenant was written as deleted. NULL means the row
-- is not a tombstone. The status column stays the state a request is resolved
-- by; this column records when the terminal state was reached, which is what the
-- later cleanup reads.
--
-- The index serves the owner lookup: whether an account owns an active business
-- organization is a seek rather than a scan of the roster.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS owner_account TEXT NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ NULL;
CREATE INDEX IF NOT EXISTS tenants_owner_account_idx ON tenants (owner_account);
