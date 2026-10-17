package metadata

import (
	"testing"

	"github.com/vislake/speed/pkg/feature"
)

// TestTheModuleDeclaresTheObjectsFeature holds the declaration the frontend
// gates the object surface on: one static Spec with the exact name.
func TestTheModuleDeclaresTheObjectsFeature(t *testing.T) {
	want := feature.Spec{Name: "objectstore.objects"}
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
