//go:build integration

package catalog

import "testing"

// TestASubqueryWithoutATenantFailsTheStatementOnPostgres checks that a subquery
// without a tenant fails the statement on PostgreSQL.
func TestASubqueryWithoutATenantFailsTheStatementOnPostgres(t *testing.T) {
	handle := openFilteredPostgres(t)
	seed(t, handle)
	requireSubqueryRefusal(t, handle)
}

// TestARowWithoutATenantReportsTheRefusalThroughItsScanOnPostgres checks that a
// refused Row reports through its Scan on PostgreSQL.
func TestARowWithoutATenantReportsTheRefusalThroughItsScanOnPostgres(t *testing.T) {
	handle := openFilteredPostgres(t)
	seed(t, handle)
	requireRowRefusal(t, handle)
}

// TestARefusedRowTakesNoConnectionOnPostgres checks that a refused Row takes no
// connection on PostgreSQL.
func TestARefusedRowTakesNoConnectionOnPostgres(t *testing.T) {
	handle := openFilteredPostgres(t)
	seed(t, handle)
	requireRowRefusedWithoutAConnection(t, handle)
}
