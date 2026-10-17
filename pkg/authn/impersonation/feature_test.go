package impersonation

import (
	"testing"

	"github.com/vislake/speed/pkg/feature"
)

// TestTheModuleDeclaresTheImpersonationFeature holds the declaration the
// frontend gates the impersonation routes on: one static Spec with the exact
// name.
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
