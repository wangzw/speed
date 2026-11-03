-- The distinct-destination dimension counts the different numbers one source
-- address sent to in a window, so its count walks (source_mac, phone_mac,
-- created_at). The attempt dimensions need no new column: a failed attempt is a
-- refunded row, and the existing (source_mac, created_at) and (purpose,
-- suppressed, created_at) indexes answer them, the latter because the
-- deployment-wide attempt budget keeps the send budget's unsuppressed-only rule.
CREATE INDEX IF NOT EXISTS authn_challenges_source_phone
    ON authn_challenges (source_mac, phone_mac, created_at);
