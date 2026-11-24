package self

import (
	"errors"
	"slices"
	"testing"
)

// TestTheEndpointListRefusesEveryShapeItCannotRunOn checks that an empty list, an empty
// name, "*" and a repeated name are each refused with the key named.
func TestTheEndpointListRefusesEveryShapeItCannotRunOn(t *testing.T) {
	cases := map[string][]string{
		"empty":            nil,
		"an empty name":    {""},
		"every endpoint":   {allEndpoints},
		"a repeated name":  {"account", "account"},
		"a name and empty": {"account", ""},
	}
	for name, endpoints := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := (moduleConfig{Endpoints: endpoints}).resolve(); !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("resolve(%v) gave %v, want ErrInvalidConfig", endpoints, err)
			}
		})
	}
}

// TestTheEndpointListKeepsConcreteNames checks that concrete names are kept in the order given.
func TestTheEndpointListKeepsConcreteNames(t *testing.T) {
	endpoints := []string{"account", "api"}
	got, err := (moduleConfig{Endpoints: endpoints}).resolve()
	if err != nil {
		t.Fatalf("resolve(%v): %v", endpoints, err)
	}
	if !slices.Equal(got.endpoints, endpoints) {
		t.Errorf("resolve(%v) = %v, want the same list", endpoints, got.endpoints)
	}
}
