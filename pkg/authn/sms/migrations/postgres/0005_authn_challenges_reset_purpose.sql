-- The reset purpose joins the closed set a challenge row may carry: an anonymous
-- password reset proves the holder of a number through the same state machine as
-- the other purposes, so its rows share the table. A reset row fills no account
-- and takes no account lock, but that is the caller's shape, not the column's.
--
-- The original column check was declared without a name inside CREATE TABLE, so
-- PostgreSQL named it authn_challenges_purpose_check after the table and column.
-- Replacing that constraint is enough: the columns and indexes are unchanged.
ALTER TABLE authn_challenges DROP CONSTRAINT IF EXISTS authn_challenges_purpose_check;

ALTER TABLE authn_challenges
    ADD CONSTRAINT authn_challenges_purpose_check
    CHECK (purpose IN ('login', 'mfa', 'enroll', 'reset'));
