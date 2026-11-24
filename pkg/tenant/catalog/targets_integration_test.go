//go:build integration

package catalog

import "testing"

func TestAKeyPostgresReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, openFilteredPostgres, keyedWrites, []string{`"tenant_id"`})
}

func TestARawTargetPostgresReadsAsTheTenantColumnIsJudged(t *testing.T) {
	requireEveryShapeRefused(t, openFilteredPostgres, rawWrites,
		[]string{" tenant_id", "TeNaNt_Id", `"tenant_id"`, "tenant_id = 'globex', note"})
}

func TestAnUpdateFromAnotherTypeIsJudgedByTheColumnItWritesOnPostgres(t *testing.T) {
	requirePatchesRefused(t, openFilteredPostgres, []string{`"tenant_id"`, "tenant_id"})
}
