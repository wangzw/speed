package profile

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

// TestTheProfileViewCarriesNoIdentifier holds the view to the external name: a
// client addresses the tenant by the alias, and the internal identifier never
// reaches it.
func TestTheProfileViewCarriesNoIdentifier(t *testing.T) {
	encoded, err := json.Marshal(profileViewOf(tenant.Tenant{ID: "t_acme", Alias: "acme"}))
	if err != nil {
		t.Fatalf("encoding the view: %v", err)
	}
	if strings.Contains(string(encoded), `"id"`) {
		t.Errorf("the view carries an id: %s", encoded)
	}
}
