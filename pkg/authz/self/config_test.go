package self

import (
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// The endpoint list is required and takes concrete names only: a list that
// mounted the read nowhere, an empty name, the every-endpoint name and a
// repeated name are each refused.
func TestTheEndpointListRefusesEveryShapeItCannotRunOn(t *testing.T) {
	cases := map[string][]string{
		"empty":            nil,
		"an empty name":    {""},
		"every endpoint":   {allEndpoints},
		"a repeated name":  {"public", "public"},
		"a name and empty": {"public", ""},
	}
	for name, endpoints := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := (moduleConfig{Endpoints: endpoints}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
				t.Errorf("resolve(%v) gave %v, want ErrInvalidConfig", endpoints, err)
			}
		})
	}
}

// A list of concrete names is what the module runs on, in the order given.
func TestTheEndpointListKeepsConcreteNames(t *testing.T) {
	endpoints := []string{"admin", "api"}
	got, err := (moduleConfig{Endpoints: endpoints}).resolve()
	if err != nil {
		t.Fatalf("resolve(%v): %v", endpoints, err)
	}
	if !slices.Equal(got.endpoints, endpoints) {
		t.Errorf("resolve(%v) = %v, want the same list", endpoints, got.endpoints)
	}
}
