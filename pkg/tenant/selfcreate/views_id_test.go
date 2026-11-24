package selfcreate

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTheCreatedOrganizationCarriesNoIdentifier checks that the creation answer
// carries no generated identifier.
func TestTheCreatedOrganizationCarriesNoIdentifier(t *testing.T) {
	encoded, err := json.Marshal(OrganizationCreatedTenant{Name: "acme", Status: "active"})
	if err != nil {
		t.Fatalf("encoding the view: %v", err)
	}
	if strings.Contains(string(encoded), `"id"`) {
		t.Errorf("the view carries an id: %s", encoded)
	}
}
