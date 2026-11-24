-- The reset purpose joins the closed set. the original unnamed check is PostgreSQL's authn_challenges_purpose_check.
ALTER TABLE authn_challenges DROP CONSTRAINT IF EXISTS authn_challenges_purpose_check;
ALTER TABLE authn_challenges
    ADD CONSTRAINT authn_challenges_purpose_check
    CHECK (purpose IN ('login', 'mfa', 'enroll', 'reset'));
