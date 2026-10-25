-- The source dimension of a send: the request's client address, kept only as
-- the same keyed digest the number uses, so the column reveals nothing without
-- the root key. The plaintext address is never written.
--
-- The column is nullable, and its null is meaningful: a send with no trustworthy
-- origin has no source, takes no source lock and spends no source budget. Making
-- an empty value a source would put every such send under one key.
--
-- The old rows keep the null, with no backfill: the address that produced them
-- was never stored anywhere to recover.
ALTER TABLE authn_challenges ADD COLUMN IF NOT EXISTS source_mac TEXT NULL;

-- The source dimension of every budget walks (source_mac, created_at).
CREATE INDEX IF NOT EXISTS authn_challenges_source ON authn_challenges (source_mac, created_at);
