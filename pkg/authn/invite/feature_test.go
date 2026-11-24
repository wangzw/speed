package invite

import (
	"testing"

	"github.com/vislake/speed/pkg/feature"
)

// TestTheModuleDeclaresTheInvitationsFeature checks that the module declares one static authn.invitations feature spec.
func TestTheModuleDeclaresTheInvitationsFeature(t *testing.T) {
	want := feature.Spec{Name: "authn.invitations"}
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
