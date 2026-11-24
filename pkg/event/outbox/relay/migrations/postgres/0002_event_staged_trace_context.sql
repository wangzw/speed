-- The tracing context of the span the staging write was in, in dispatch's text form. The relay restores it so a subscriber's span descends from that write's span; a table built before this column reads the default as an empty carrier.
ALTER TABLE event_staged ADD COLUMN IF NOT EXISTS trace_context TEXT NOT NULL DEFAULT '';
