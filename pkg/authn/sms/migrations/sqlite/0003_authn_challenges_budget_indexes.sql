-- The deployment-wide budget counts the unsuppressed rows of one purpose in a
-- window, so its count and its oldest-row read walk (purpose, suppressed,
-- created_at).
CREATE INDEX IF NOT EXISTS authn_challenges_purpose_suppressed
    ON authn_challenges (purpose, suppressed, created_at);

-- The account dimension of the mfa and enroll budgets walks (account_id,
-- created_at).
CREATE INDEX IF NOT EXISTS authn_challenges_account
    ON authn_challenges (account_id, created_at);
