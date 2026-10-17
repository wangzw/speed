package inbox

import (
	"testing"

	"github.com/vislake/speed/pkg/feature"
)

// TestTheModuleDeclaresTheInboxFeature holds the declaration the frontend gates
// the inbox surface on: one static Spec with the exact name.
func TestTheModuleDeclaresTheInboxFeature(t *testing.T) {
	want := feature.Spec{Name: "notification.inbox"}
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
