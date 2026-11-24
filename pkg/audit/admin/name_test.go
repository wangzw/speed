package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/audit/journal"
)

// TestTheRecordViewNamesTheTenantName holds the read surface to the same
// rule the write side follows.
func TestTheRecordViewNamesTheTenantName(t *testing.T) {
	view := recordViewOf(journal.Row{Tenant: "t1", Action: "a.update"}, "acme")
	if view.Tenant != "acme" {
		t.Errorf("the record view names %q, want the name acme", view.Tenant)
	}
	if blank := recordViewOf(journal.Row{}, ""); blank.Tenant != "" {
		t.Errorf("a row belonging to no tenant names %q, want empty", blank.Tenant)
	}
}
