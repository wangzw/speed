//go:build integration

package catalog

import "testing"

// TestAnInsertWithNoFilterAndNoTenantFailsOnPostgres checks that an insert with no
// filter and no tenant fails on PostgreSQL.
func TestAnInsertWithNoFilterAndNoTenantFailsOnPostgres(t *testing.T) {
	requireUnfilteredInsertRefused(t, openUnfilteredPostgres(t))
}
