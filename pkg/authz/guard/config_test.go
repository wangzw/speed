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

func readConfigBody(t *testing.T, body string) (resolved, error) {
	t.Helper()
	reader := authztest.ConfigReader(t, core.Module{Name: ModuleName, Resources: []any{schema()}}, body)
	return readConfig(reader)
}

func endpointsOnly(list string) string {
	return `{"authz":{"guard":{"endpoints":` + list + `}}}`
}

func withUndeclared(list, undeclared string) string {
	return `{"authz":{"guard":{"endpoints":` + list + `,"undeclared":"` + undeclared + `"}}}`
}

// TestReadConfigFailsWithoutTheEndpointList checks that read config fails without the endpoint list.
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

// TestReadConfigRefusesAnEndpointListItCannotRunOn checks that read config refuses an endpoint list it cannot run on.
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

// TestReadConfigRefusesAnEndpointNamedTwice checks that read config refuses an endpoint named twice.
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

// TestTheUndeclaredDefaultIsClosedAndTheSetIsClosed checks that the undeclared default is closed and the set is closed.
func TestTheUndeclaredDefaultIsClosedAndTheSetIsClosed(t *testing.T) {
	got, err := readConfigBody(t, endpointsOnly(`["public"]`))
	if err != nil {
		t.Fatalf("readConfig with no undeclared item: %v", err)
	}
	if got.undeclared != undeclaredClosed {
		t.Fatalf("the default undeclared disposition is %v, want closed", got.undeclared)
	}

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

// TestReadConfigAcceptsDistinctConcreteNames checks that read config accepts distinct concrete names.
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

// TestTheGuardInputsAreReadFromThePrimarySourceAlone checks that the guard inputs are read from the primary source alone.
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

// TestNoDefaultForTheEndpointListAndAClosedDefaultForUndeclared checks that no default for the endpoint list and a closed default for undeclared.
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
