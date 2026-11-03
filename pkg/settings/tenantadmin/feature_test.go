package tenantadmin

import (
	"testing"

	"github.com/vislake/speed/pkg/feature"
)

// TestTheModuleDeclaresTheTenantSettingsFeature holds the declaration the
// frontend gates the tenant settings surface on: one static Spec with the exact
// name.
func TestTheModuleDeclaresTheTenantSettingsFeature(t *testing.T) {
	want := feature.Spec{Name: "settings.tenantadmin"}
	var got []feature.Spec
	for _, r := range Module().Resources {
		if s, ok := r.(feature.Spec); ok {
			got = append(got, s)
		}
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the module declares %+v, want exactly %+v", got, want)
	}
}
