package guard

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

// The configuration cases drive the real loader over a real body, so each item
// reads as an assembly reads it. This module's section is declared here by the
// schema under test, since the descriptor's resources are not this file's to
// hold and a stub reader would agree with any declaration whatsoever.

// readConfigBody decodes and resolves this module's section out of body through
// the real loader.
func readConfigBody(t *testing.T, body string) (resolved, error) {
	t.Helper()
	reader := authztest.ConfigReader(t, core.Module{Name: ModuleName, Resources: []any{schema()}}, body)
	return readConfig(reader)
}

// endpointsOnly is a section carrying an endpoint list and no undeclared item,
// so the item's declared default is what the case reads back.
func endpointsOnly(list string) string {
	return `{"authz":{"guard":{"endpoints":` + list + `}}}`
}

// withUndeclared is a section carrying both items, undeclared written even when
// empty.
func withUndeclared(list, undeclared string) string {
	return `{"authz":{"guard":{"endpoints":` + list + `,"undeclared":"` + undeclared + `"}}}`
}

// TestReadConfigFailsWithoutTheEndpointList holds that the key is required
// with no default, and that the failure is the decoder's own, which names
// the path.
func TestReadConfigFailsWithoutTheEndpointList(t *testing.T) {
	for name, body := range map[string]string{
		"no section at all":        `{}`,
		"the section empty":        `{"authz":{"guard":{}}}`,
		"only the undeclared item": `{"authz":{"guard":{"undeclared":"open"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := readConfigBody(t, body)
			if err == nil {
				t.Fatalf("readConfig with no endpoint list = %+v, want a failure", got)
			}
			if got.endpoints != nil {
				t.Fatalf("a failed read carried endpoints %v, want none", got.endpoints)
			}
			if !errors.Is(err, config.ErrMissingRequired) {
				t.Fatalf("the failure is not config.ErrMissingRequired: %v", err)
			}
			if !strings.Contains(err.Error(), endpointsKey) {
				t.Fatalf("the failure does not name the key that is missing: %v", err)
			}
		})
	}
}

// TestReadConfigRefusesAnEndpointListItCannotRunOn holds the three shapes
// that are authn.ErrInvalidConfig, each naming the key.
func TestReadConfigRefusesAnEndpointListItCannotRunOn(t *testing.T) {
	for name, list := range map[string]string{
		"an empty list":            `[]`,
		"an empty name":            `["public", ""]`,
		"only an empty name":       `[""]`,
		"the name for every end":   `["*"]`,
		"the star beside a name":   `["public", "*"]`,
		"a name before the star":   `["*", "public"]`,
		"the star written twice":   `["*", "*"]`,
		"an empty name written 2x": `["", ""]`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := readConfigBody(t, endpointsOnly(list))
			if err == nil {
				t.Fatalf("readConfig over %s = %+v, want a failure", list, got)
			}
			if got.endpoints != nil {
				t.Fatalf("a failed read carried endpoints %v, want none", got.endpoints)
			}
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("the failure is not authn.ErrInvalidConfig: %v", err)
			}
			if !strings.Contains(err.Error(), endpointsKey) {
				t.Fatalf("the failure does not name the key: %v", err)
			}
		})
	}
}

// TestReadConfigRefusesAnEndpointNamedTwice holds the duplicate, with the
// endpoint and the key in the message and the namespace once.
func TestReadConfigRefusesAnEndpointNamedTwice(t *testing.T) {
	got, err := readConfigBody(t, endpointsOnly(`["public", "admin", "public"]`))
	if err == nil {
		t.Fatalf("readConfig over a list naming public twice = %+v, want a failure", got)
	}
	if got.endpoints != nil {
		t.Fatalf("a failed read carried endpoints %v, want none", got.endpoints)
	}
	if !errors.Is(err, tenant.ErrDuplicateEndpoint) {
		t.Fatalf("the failure is not tenant.ErrDuplicateEndpoint: %v", err)
	}
	if !strings.Contains(err.Error(), `"public"`) || !strings.Contains(err.Error(), endpointsKey) {
		t.Fatalf("the failure does not name the endpoint and the key: %v", err)
	}
	if n := strings.Count(err.Error(), ConfigNamespace); n != 1 {
		t.Fatalf("the failure names %s %d times, want once: %v", ConfigNamespace, n, err)
	}
}

// TestTheUndeclaredDefaultIsClosedAndTheSetIsClosed holds the disposition:
// with nothing written it is closed, the two written values read back, and
// every other value is refused rather than read towards a disposition.
func TestTheUndeclaredDefaultIsClosedAndTheSetIsClosed(t *testing.T) {
	// Nothing written: the declared default, closed.
	got, err := readConfigBody(t, endpointsOnly(`["public"]`))
	if err != nil {
		t.Fatalf("readConfig with no undeclared item: %v", err)
	}
	if got.undeclared != undeclaredClosed {
		t.Fatalf("the default undeclared disposition is %v, want closed", got.undeclared)
	}

	// The two values in the set read back as themselves.
	for value, want := range map[string]undeclaredMode{
		closedValue: undeclaredClosed,
		openValue:   undeclaredOpen,
	} {
		got, err := readConfigBody(t, withUndeclared(`["public"]`, value))
		if err != nil {
			t.Fatalf("readConfig with undeclared %q: %v", value, err)
		}
		if got.undeclared != want {
			t.Fatalf("undeclared %q read as %v, want %v", value, got.undeclared, want)
		}
	}

	// Every value outside the set is refused, the key named.
	for _, value := range []string{"true", "OPEN", "Open", "refuse", "on", "open,closed"} {
		got, err := readConfigBody(t, withUndeclared(`["public"]`, value))
		if err == nil {
			t.Fatalf("readConfig with undeclared %q = %+v, want a failure", value, got)
		}
		if got.endpoints != nil {
			t.Fatalf("a failed read carried endpoints %v, want none", got.endpoints)
		}
		if !errors.Is(err, authn.ErrInvalidConfig) {
			t.Fatalf("undeclared %q gave %v, want authn.ErrInvalidConfig", value, err)
		}
		if !strings.Contains(err.Error(), undeclaredKey) {
			t.Fatalf("the failure does not name the key: %v", err)
		}
	}
}

// TestReadConfigAcceptsDistinctConcreteNames is the other half of the rules
// above: a list of distinct concrete names runs, and leaves the undeclared
// disposition at its default.
func TestReadConfigAcceptsDistinctConcreteNames(t *testing.T) {
	got, err := readConfigBody(t, endpointsOnly(`["public", "admin"]`))
	if err != nil {
		t.Fatalf("readConfig over two distinct endpoints: %v", err)
	}
	if !slices.Equal(got.endpoints, []string{"public", "admin"}) {
		t.Fatalf("the resolved endpoints are %v, want public and admin", got.endpoints)
	}
	if got.undeclared != undeclaredClosed {
		t.Fatalf("the undeclared disposition is %v, want the closed default", got.undeclared)
	}
}

// TestTheGuardInputsAreReadFromThePrimarySourceAlone holds the declaration
// of the two keys: the endpoint list required, and both items settable
// nowhere but the primary source.
func TestTheGuardInputsAreReadFromThePrimarySourceAlone(t *testing.T) {
	s := schema()
	if s.Namespace != ModuleName {
		t.Errorf("the namespace is %q, want the module's own name", s.Namespace)
	}
	if len(s.Items) != 2 {
		t.Fatalf("the schema declares %d items, want the two keys", len(s.Items))
	}

	list, declared := s.Items[endpointsItem]
	if !declared {
		t.Fatalf("the schema declares no item %q", endpointsItem)
	}
	if !list.Required {
		t.Error("the endpoint list is not required: an unwritten list would guard nothing")
	}
	if list.Origins != config.OriginPrimary {
		t.Errorf("the endpoint list takes origins %v, want the primary source alone", list.Origins)
	}

	undeclared, declared := s.Items[undeclaredItem]
	if !declared {
		t.Fatalf("the schema declares no item %q", undeclaredItem)
	}
	if undeclared.Origins != config.OriginPrimary {
		t.Errorf("the undeclared item takes origins %v, want the primary source alone", undeclared.Origins)
	}
}

// TestNoDefaultForTheEndpointListAndAClosedDefaultForUndeclared holds the
// declared defaults: no list, and a closed disposition.
func TestNoDefaultForTheEndpointListAndAClosedDefaultForUndeclared(t *testing.T) {
	if len(configDefaults.Endpoints) != 0 {
		t.Fatalf("the declared default list is %v, want none", configDefaults.Endpoints)
	}
	if configDefaults.Undeclared != closedValue {
		t.Fatalf("the declared default for undeclared is %q, want %q", configDefaults.Undeclared, closedValue)
	}
	var zero resolved
	if zero.undeclared != undeclaredClosed {
		t.Fatalf("the zero resolved value is %v, want closed", zero.undeclared)
	}
}
