package selfcreate

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTheCreatedOrganizationCarriesNoIdentifier holds the creation answer to
// the external name: the generated identifier is internal.
func TestTheCreatedOrganizationCarriesNoIdentifier(t *testing.T) {
	encoded, err := json.Marshal(OrganizationCreatedTenant{Alias: "acme", Status: "active"})
	if err != nil {
		t.Fatalf("encoding the view: %v", err)
	}
	if strings.Contains(string(encoded), `"id"`) {
		t.Errorf("the view carries an id: %s", encoded)
	}
}
