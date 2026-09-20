-- The migration the migrate-command scenario applies. It runs on SQLite so
-- that the one case about a drive that stops at the migration stage needs no
-- container: the depth is a property of the command tree, and gating the only
-- case that observes it on docker would leave it unobserved wherever docker is
-- absent.
--
-- It creates the same table the PostgreSQL file does and nothing else. The
-- session bookkeeping that file carries belongs to the concurrency scenario,
-- which is about two processes and therefore about a server they both reach.
CREATE TABLE widgets (id INTEGER PRIMARY KEY);
