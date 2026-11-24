-- The deployment-wide budget walks (purpose, suppressed, created_at). the account dimension walks (account_id, created_at).
CREATE INDEX IF NOT EXISTS authn_challenges_purpose_suppressed
    ON authn_challenges (purpose, suppressed, created_at);
CREATE INDEX IF NOT EXISTS authn_challenges_account
    ON authn_challenges (account_id, created_at);
