//go:build integration

package catalog

import "testing"

// TestASubqueryWithoutATenantFailsTheStatementOnPostgres checks on PostgreSQL
// that a refusal in a subquery built without a tenant fails the enclosing
// statement.
func TestASubqueryWithoutATenantFailsTheStatementOnPostgres(t *testing.T) {
	handle := openFilteredPostgres(t)
	seed(t, handle)
	requireSubqueryRefusal(t, handle)
}

// TestARowWithoutATenantReportsTheRefusalThroughItsScanOnPostgres checks on
// PostgreSQL that a refused Row reports the refusal through its Scan.
func TestARowWithoutATenantReportsTheRefusalThroughItsScanOnPostgres(t *testing.T) {
	handle := openFilteredPostgres(t)
	seed(t, handle)
	requireRowRefusal(t, handle)
}

// TestARefusedRowTakesNoConnectionOnPostgres checks on PostgreSQL that a
// refused Row is answered at once and takes no pool connection.
func TestARefusedRowTakesNoConnectionOnPostgres(t *testing.T) {
	handle := openFilteredPostgres(t)
	seed(t, handle)
	requireRowRefusedWithoutAConnection(t, handle)
}
