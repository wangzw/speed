-- The sleep runs inside the migration's transaction, holding the mutex long
-- enough for the second replica to reach its own acquisition. The appliers row
-- is what lets the run tell which replica applied the migration.
SELECT pg_sleep(3);
CREATE TABLE widgets (id INTEGER PRIMARY KEY);
CREATE TABLE dbhost_appliers (role TEXT NOT NULL);
INSERT INTO dbhost_appliers (role) VALUES (current_setting('application_name'));
