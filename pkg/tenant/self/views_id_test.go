package self

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTheSelfTenantCarriesNoIdentifier holds the caller's tenant entry to the
// external name: the alias and the kind are what a client branches on.
func TestTheSelfTenantCarriesNoIdentifier(t *testing.T) {
	encoded, err := json.Marshal(tenantSelfTenant{Alias: "alice", Kind: "personal"})
	if err != nil {
		t.Fatalf("encoding the view: %v", err)
	}
	if strings.Contains(string(encoded), `"id"`) {
		t.Errorf("the view carries an id: %s", encoded)
	}
}
