-- The authenticator app is one entry per account. An account that already
-- holds several active ones (registered before the rule existed) keeps the
-- most recently activated row, and every other active authenticator row becomes
-- disabled, kept with its kind's data for the audit trail rather than
-- deleted. The statement is idempotent: a second run finds no active row with
-- a newer one beside it and changes nothing.
UPDATE authn_factors
SET status = 2
WHERE authn_factors.kind = 'totp'
  AND authn_factors.status = 1
  AND EXISTS (
    SELECT 1 FROM authn_factors AS newer
    WHERE newer.account_id = authn_factors.account_id
      AND newer.kind = 'totp'
      AND newer.status = 1
      AND (
        COALESCE(newer.activated_at, '0001-01-01 00:00:00') > COALESCE(authn_factors.activated_at, '0001-01-01 00:00:00')
        OR (
          COALESCE(newer.activated_at, '0001-01-01 00:00:00') = COALESCE(authn_factors.activated_at, '0001-01-01 00:00:00')
          AND newer.id > authn_factors.id
        )
      )
  );
