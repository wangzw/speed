-- The tracing context of the span the staging write was in, serialized in the
-- text form dispatch keeps it in. The relay restores it onto the event at
-- publish, so a subscriber's span descends from that write's span rather than
-- from the relay's. A table built before this column existed keeps its rows,
-- which read the default empty value as an empty carrier.
ALTER TABLE event_staged ADD COLUMN trace_context TEXT NOT NULL DEFAULT '';
