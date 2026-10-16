//go:build integration

package catalog

import "testing"

func TestASubqueryWithoutATenantFailsTheStatementOnPostgres(t *testing.T) {
	handle := openFilteredPostgres(t)
	seed(t, handle)
	requireSubqueryRefusal(t, handle)
}

func TestARowWithoutATenantReportsTheRefusalThroughItsScanOnPostgres(t *testing.T) {
	handle := openFilteredPostgres(t)
	seed(t, handle)
	requireRowRefusal(t, handle)
}

func TestARefusedRowTakesNoConnectionOnPostgres(t *testing.T) {
	handle := openFilteredPostgres(t)
	seed(t, handle)
	requireRowRefusedWithoutAConnection(t, handle)
}
