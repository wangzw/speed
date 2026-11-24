-- A row the carrier certainly did not take is refunded and spent, so every budget stops counting it. an uncertain outcome is not refunded.
ALTER TABLE authn_challenges ADD COLUMN IF NOT EXISTS refunded_at TIMESTAMPTZ NULL;
