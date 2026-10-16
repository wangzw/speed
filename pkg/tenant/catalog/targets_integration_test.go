//go:build integration

package catalog

import "testing"

// PostgreSQL reads a name carrying its own double quotes as that quoted name.
func TestAKeyPostgresReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, openFilteredPostgres, keyedWrites, []string{`"tenant_id"`})
}

// PostgreSQL folds a bare raw name to lower case and skips the blanks around
// it.
func TestARawTargetPostgresReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, openFilteredPostgres, rawWrites,
		[]string{" tenant_id", "TeNaNt_Id", `"tenant_id"`, "tenant_id = 'globex', note"})
}

func TestAnUpdateFromAnotherTypeIsJudgedByTheColumnItWritesOnPostgres(t *testing.T) {
	requirePatchesRefused(t, openFilteredPostgres, []string{`"tenant_id"`, "tenant_id"})
}
