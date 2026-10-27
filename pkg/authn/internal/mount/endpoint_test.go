package mount

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

const endpointKey = "authn.gate.mount.endpoints.default"

// TestEndpointIsTheConfiguredOneOrTheOnlyOneDeclared holds the rule the module
// that mounts routes and the gateway both read: a name that was written wins over
// whatever the entry point declares, and an empty one takes the only endpoint
// there is.
func TestEndpointIsTheConfiguredOneOrTheOnlyOneDeclared(t *testing.T) {
	for _, c := range []struct {
		name       string
		configured string
		declared   []string
		want       string
	}{
		{"a name written, one declared", "public", []string{"public"}, "public"},
		{"a name written, several declared", "admin", []string{"admin", "public"}, "admin"},
		{"nothing written, one declared", "", []string{"public"}, "public"},
		// Whether the name exists is the caller's to judge, with its own
		// sentinel; this function only says where the routes go.
		{"a name written that is not declared", "typo", []string{"public"}, "typo"},
		{"a name written, none declared", "public", nil, "public"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Endpoint(c.configured, c.declared, endpointKey)
			if err != nil {
				t.Fatalf("Endpoint failed: %v", err)
			}
			if got != c.want {
				t.Errorf("Endpoint(%q, %v) = %q, want %q", c.configured, c.declared, got, c.want)
			}
		})
	}
}

// TestEndpointLeftEmptyWithNoOneToPickIsRefused holds the failure: nothing
// written and an entry point that declares none or several endpoints is a
// startup error that names the key to write and, for several, the endpoints to
// choose among. A first-listed default would be a guess about which traffic the
// authentication routes share a listener with.
func TestEndpointLeftEmptyWithNoOneToPickIsRefused(t *testing.T) {
	for _, c := range []struct {
		name     string
		declared []string
		names    []string
	}{
		{"none declared", nil, nil},
		{"two declared", []string{"admin", "public"}, []string{"admin", "public"}},
		{"three declared", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := Endpoint("", c.declared, endpointKey)
			if !errors.Is(err, authn.ErrRoutesUnconfigured) {
				t.Fatalf("Endpoint gave %q, %v, want an error wrapping %v", got, err, authn.ErrRoutesUnconfigured)
			}
			if got != "" {
				t.Errorf("Endpoint named %q alongside the error", got)
			}
			if !strings.Contains(err.Error(), endpointKey) {
				t.Errorf("the error %q does not name the key to write", err)
			}
			for _, name := range c.names {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("the error %q does not name the endpoint %q to choose", err, name)
				}
			}
		})
	}
}
