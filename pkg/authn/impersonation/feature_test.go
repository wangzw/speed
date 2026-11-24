package impersonation

import (
	"testing"

	"github.com/vislake/speed/pkg/feature"
)

// TestTheModuleDeclaresTheImpersonationFeature checks the one static feature declaration.
func TestTheModuleDeclaresTheImpersonationFeature(t *testing.T) {
	want := feature.Spec{Name: "authn.impersonation"}
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
