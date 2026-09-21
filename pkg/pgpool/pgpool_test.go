package pgpool

import (
	"strings"
	"testing"
)

// The dsn item's description is what the help output prints for it, and it is
// the only place an operator is told what leaving the key out does. An
// implementation reaches PostgreSQL at the server this key names, and it is in
// the run only because the command tree named it, so the description has to say
// the startup fails — not that the assembly goes on without the backend.
func TestDSNDescriptionStatesTheStartupFails(t *testing.T) {
	description := Items()[DSNKey].Description
	if description == "" {
		t.Fatalf("Items() gives no description for %q, and the help output prints one", DSNKey)
	}
	if strings.Contains(description, "without this backend") {
		t.Errorf("the description of %q still promises that leaving it out runs the assembly "+
			"without this backend: %q", DSNKey, description)
	}
	for _, want := range []string{"fails the startup", "command tree"} {
		if !strings.Contains(description, want) {
			t.Errorf("the description of %q does not say %q: %q", DSNKey, want, description)
		}
	}
}
