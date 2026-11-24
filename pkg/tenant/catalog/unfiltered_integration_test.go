//go:build integration

package catalog

import "testing"

// TestAnInsertWithNoFilterAndNoTenantFailsOnPostgres checks on PostgreSQL that,
// with no filter, an insert with no tenant fails where the empty identifier is
// bound.
func TestAnInsertWithNoFilterAndNoTenantFailsOnPostgres(t *testing.T) {
	requireUnfilteredInsertRefused(t, openUnfilteredPostgres(t))
}
