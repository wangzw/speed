package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/audit/journal"
)

// TestTheRecordViewNamesTheAlias holds the read surface to the same rule the
// write side follows: a row carries the internal identifier, and the view
// answers the alias a reader can act on.
func TestTheRecordViewNamesTheAlias(t *testing.T) {
	view := recordViewOf(journal.Row{Tenant: "t1", Action: "a.update"}, "acme")
	if view.Tenant != "acme" {
		t.Errorf("the record view names %q, want the alias acme", view.Tenant)
	}
	if blank := recordViewOf(journal.Row{}, ""); blank.Tenant != "" {
		t.Errorf("a row belonging to no tenant names %q, want empty", blank.Tenant)
	}
}
