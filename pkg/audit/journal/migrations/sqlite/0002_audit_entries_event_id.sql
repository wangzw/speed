ALTER TABLE audit_entries ADD COLUMN event_id TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS audit_entries_event_id_key ON audit_entries (event_id);
