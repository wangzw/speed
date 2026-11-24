-- SQLite serves the migrate-command scenario so its depth case needs no
-- container. It creates the same widgets table and no concurrency bookkeeping.
CREATE TABLE widgets (id INTEGER PRIMARY KEY);
