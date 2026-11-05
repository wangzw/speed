-- The listing of one tenant narrowed by state, newest first. The status column
-- holds the stored state. The two pending views share status = 0 and differ in
-- the deadline predicate, so the tenant and the state narrow the rows and the
-- creation time orders them.
CREATE INDEX IF NOT EXISTS authn_invitations_tenant_status_created
    ON authn_invitations (tenant_id, status, created_at);
