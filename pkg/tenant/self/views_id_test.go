package self

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTheSelfTenantCarriesNoIdentifier(t *testing.T) {
	encoded, err := json.Marshal(tenantSelfTenant{Name: "alice", Kind: "personal"})
	if err != nil {
		t.Fatalf("encoding the view: %v", err)
	}
	if strings.Contains(string(encoded), `"id"`) {
		t.Errorf("the view carries an id: %s", encoded)
	}
}
