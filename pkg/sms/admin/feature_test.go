package admin

import (
	"testing"

	"github.com/vislake/speed/pkg/feature"
)

// TestTheModuleDeclaresTheSMSAdminFeature checks that the module declares one static feature.Spec with the exact name.
func TestTheModuleDeclaresTheSMSAdminFeature(t *testing.T) {
	want := feature.Spec{Name: "sms.admin"}
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
