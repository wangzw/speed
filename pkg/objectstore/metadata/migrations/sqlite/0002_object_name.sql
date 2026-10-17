-- The objects table gains the name a caller declared, normalized once at
-- declaration. It is a label rather than an identifier: it is not unique, no
-- lookup reads it and it takes no index.
--
-- The column is added with an empty-string default rather than as nullable, so
-- a row always carries a value and the view always has one to show. Rows that
-- predate this migration keep the empty string: there is nothing to backfill
-- from, because the declaration's name was never stored before.
ALTER TABLE objects ADD COLUMN name TEXT NOT NULL DEFAULT '';
