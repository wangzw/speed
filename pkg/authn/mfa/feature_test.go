package mfa

import (
	"testing"

	"github.com/vislake/speed/pkg/feature"
)

// TestTheModuleDeclaresTheMultiFactorFeature holds the declaration the frontend
// gates the multi-factor section on: one static Spec with the exact name.
func TestTheModuleDeclaresTheMultiFactorFeature(t *testing.T) {
	want := feature.Spec{Name: "authn.mfa"}
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
