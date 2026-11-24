//go:build integration

package catalog

import "testing"

// TestAKeyPostgresReadsAsTheTenantColumnIsJudged checks that a name carrying
// its own double quotes is read as that quoted name.
func TestAKeyPostgresReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, openFilteredPostgres, keyedWrites, []string{`"tenant_id"`})
}

// TestARawTargetPostgresReadsAsTheTenantColumnIsJudged checks that a bare raw
// name is folded to lower case and stripped of surrounding blanks.
func TestARawTargetPostgresReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, openFilteredPostgres, rawWrites,
		[]string{" tenant_id", "TeNaNt_Id", `"tenant_id"`, "tenant_id = 'globex', note"})
}

// TestAnUpdateFromAnotherTypeIsJudgedByTheColumnItWritesOnPostgres checks on
// PostgreSQL that an update from another type is judged by the column its
// matched field writes.
func TestAnUpdateFromAnotherTypeIsJudgedByTheColumnItWritesOnPostgres(t *testing.T) {
	requirePatchesRefused(t, openFilteredPostgres, []string{`"tenant_id"`, "tenant_id"})
}
