package admission

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/tenant"
)

// readThrough loads a JSON document through the real configuration path and
// hands back a reader this module's own section decodes from. The declaration is
// what is exercised: that a key nobody wrote is refused at all comes from the
// item being declared required, and a stub reader would agree with any
// declaration whatsoever.
func readThrough(t *testing.T, document string) config.Reader {
	t.Helper()
	return authntest.ConfigReader(t, core.Module{Name: ModuleName, Resources: Module().Resources}, document)
}

func endpointsConfig(list string) string {
	return `{"tenant": {"admission": {"endpoints": ` + list + `}}}`
}

// TestPrepareFailsOnAnEndpointListItCannotRunOn holds the three shapes that are
// authn.ErrInvalidConfig, each naming the key.
func TestPrepareFailsOnAnEndpointListItCannotRunOn(t *testing.T) {
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
			got, err := prepare(readThrough(t, endpointsConfig(list)))
			if err == nil {
				t.Fatalf("prepare over %s = %+v, want a startup failure", list, got)
			}
			if got != (core.Enablement{}) {
				t.Fatalf("a failed prepare carried the stance %+v, want none", got)
			}
			if !errors.Is(err, authn.ErrInvalidConfig) {
				t.Fatalf("the failure is not authn.ErrInvalidConfig: %v", err)
			}
			if !strings.Contains(err.Error(), ConfigNamespace+"."+endpointsItem) {
				t.Fatalf("the failure does not name the key: %v", err)
			}
		})
	}
}

// TestPrepareFailsOnAnEndpointNamedTwice holds the duplicate sentinel, with the
// endpoint and the key in the message and the namespace once.
func TestPrepareFailsOnAnEndpointNamedTwice(t *testing.T) {
	got, err := prepare(readThrough(t, endpointsConfig(`["public", "admin", "public"]`)))
	if err == nil {
		t.Fatalf("prepare over a list naming public twice = %+v, want a startup failure", got)
	}
	if got != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", got)
	}
	if !errors.Is(err, tenant.ErrDuplicateEndpoint) {
		t.Fatalf("the failure is not tenant.ErrDuplicateEndpoint: %v", err)
	}
	if errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("a duplicate is not an invalid configuration of the other kind: %v", err)
	}
	if !strings.Contains(err.Error(), `"public"`) || !strings.Contains(err.Error(), ConfigNamespace+"."+endpointsItem) {
		t.Fatalf("the failure does not name the endpoint and the key: %v", err)
	}
	if n := strings.Count(err.Error(), ConfigNamespace); n != 1 {
		t.Fatalf("the failure names %s %d times, want once: %v", ConfigNamespace, n, err)
	}
}

// TestPrepareFailsWithoutTheEndpointList holds that the key is required with no
// default, and that the failure is the decoder's own, which names the path.
func TestPrepareFailsWithoutTheEndpointList(t *testing.T) {
	for name, document := range map[string]string{
		"no section at all": `{}`,
		"the section empty": `{"tenant": {"admission": {}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := prepare(readThrough(t, document))
			if err == nil {
				t.Fatalf("prepare with no endpoint list = %+v, want a startup failure", got)
			}
			if got != (core.Enablement{}) {
				t.Fatalf("a failed prepare carried the stance %+v, want none: a module that cannot start must not state a stance", got)
			}
			if !errors.Is(err, config.ErrMissingRequired) {
				t.Fatalf("the failure is not config.ErrMissingRequired: %v", err)
			}
			if !strings.Contains(err.Error(), ConfigNamespace+"."+endpointsItem) {
				t.Fatalf("the failure does not name the key that is missing: %v", err)
			}
		})
	}
}

// TestPrepareAcceptsDistinctConcreteNames is the other half of the rules above.
func TestPrepareAcceptsDistinctConcreteNames(t *testing.T) {
	got, err := prepare(readThrough(t, endpointsConfig(`["public", "admin"]`)))
	if err != nil {
		t.Fatalf("prepare over two distinct endpoints: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("the stance is %v, want StateEnabled", got.State)
	}
}

// TestPrepareTellsTheTwoAbsencesApart holds that a registry the host never
// supplied a reader to is the host's omission, carried out as it stands.
func TestPrepareTellsTheTwoAbsencesApart(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	stance, err := Module().Prepare(t.Context(), reg)
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("prepare over a registry with no reader = %v, want ErrMissingProvider", err)
	}
	if stance != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", stance)
	}
}

// TestTheEndpointListIsReadFromThePrimarySourceAlone holds the declaration of the
// one key: required, and settable nowhere but the primary source. The list
// decides what is guarded, so an environment variable or a flag that could
// change it would be a way to switch the layer off for an endpoint.
func TestTheEndpointListIsReadFromThePrimarySourceAlone(t *testing.T) {
	item, declared := schema().Items[endpointsItem]
	if !declared {
		t.Fatalf("the schema declares no item %q", endpointsItem)
	}
	if !item.Required {
		t.Error("the endpoint list is not required: an unwritten list would guard nothing")
	}
	if item.Origins != config.OriginPrimary {
		t.Errorf("the endpoint list takes origins %v, want the primary source alone", item.Origins)
	}
	if schema().Namespace != ModuleName {
		t.Errorf("the namespace is %q, want the module's own name", schema().Namespace)
	}
	if len(schema().Items) != 2 {
		t.Errorf("the schema declares %d items, want the endpoint list and the SSO exemption",
			len(schema().Items))
	}
}

// TestTheCeilingIsWhatTheCodeActsOn pins the lifetime ceiling by its literal
// value. It is the bound of how long a removed member is still admitted by a
// replica that has not heard of the removal, so a test that read it off the
// constant would let the ceiling move with no failure. The declared rule and the
// description restate it, and the three are held together.
func TestTheCeilingIsWhatTheCodeActsOn(t *testing.T) {
	if maxCheckTTL != 60*time.Second {
		t.Fatalf("the lifetime ceiling is %v, want 60s", maxCheckTTL)
	}
	item, declared := settingsSchema().Items[checkTTLItem]
	if !declared {
		t.Fatalf("the settings declare no item %q", checkTTLItem)
	}
	if got := item.Domain.Rule(); got != "durations from 0 to 1m0s" {
		t.Errorf("the lifetime domain rule is %q, want it to name the 60s ceiling", got)
	}
	if !strings.Contains(item.Description, "1m0s") {
		t.Errorf("the lifetime description %q does not name the 60s ceiling", item.Description)
	}
}

// TestNoDefaultForTheEndpointList holds that the defaults carry no list: a
// default of "every endpoint" would cover the endpoint the authentication routes
// are mounted on.
func TestNoDefaultForTheEndpointList(t *testing.T) {
	if len(configDefaults.Endpoints) != 0 {
		t.Fatalf("the declared default list is %v, want none", configDefaults.Endpoints)
	}
}

// readWithResolution loads a document with this module's section and the tenant
// resolution layer's section both declared, the way a run that takes both
// modules in declares them.
func readWithResolution(t *testing.T, document string) config.Reader {
	t.Helper()
	type resolution struct {
		Endpoints []string `config:"endpoints"`
	}
	resolutionSchema := config.Schema{
		Namespace: "tenant.resolve",
		Mounts:    []config.Mount{{Value: &resolution{}}},
		Items: map[string]config.Item{
			"endpoints": {Origins: config.OriginPrimary, Required: true},
		},
	}
	return authntest.ConfigReaderAll(t, document,
		core.Module{Name: ModuleName, Resources: Module().Resources},
		core.Module{Name: "tenant.resolve.stand-in", Resources: []any{resolutionSchema}},
	)
}

// TestPrepareRefusesAnEndpointTheResolutionLayerDoesNotCover holds the subset
// rule between the two tenant layers: an endpoint this layer guards but the
// resolution layer does not is one where a request has no tenant context, and
// the layer admits a non-impersonating request without one. The startup failure
// is the only place that misconfiguration is visible, and it names both keys so
// a host knows which list to change.
func TestPrepareRefusesAnEndpointTheResolutionLayerDoesNotCover(t *testing.T) {
	body := `{"tenant":{
		"resolve":{"endpoints":["api"]},
		"admission":{"endpoints":["account","api"]}
	}}`
	got, err := prepare(readWithResolution(t, body))
	if err == nil {
		t.Fatalf("prepare over an admission endpoint the resolution layer does not cover = %+v, want a startup failure", got)
	}
	if got != (core.Enablement{}) {
		t.Fatalf("a failed prepare carried the stance %+v, want none", got)
	}
	if !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("the failure is not authn.ErrInvalidConfig: %v", err)
	}
	for _, want := range []string{ConfigNamespace + "." + endpointsItem, "tenant.resolve.endpoints", `"account"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure reads %q and does not name %q", err, want)
		}
	}
}

// TestPrepareAcceptsAnAdmissionListInsideTheResolutionList is the control: the
// same lists with the extra endpoint removed start, so the refusal above is the
// subset and not the surrounding configuration.
func TestPrepareAcceptsAnAdmissionListInsideTheResolutionList(t *testing.T) {
	body := `{"tenant":{
		"resolve":{"endpoints":["api","admin"]},
		"admission":{"endpoints":["api"]}
	}}`
	got, err := prepare(readWithResolution(t, body))
	if err != nil {
		t.Fatalf("prepare over an admission list inside the resolution list: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("the stance is %v, want StateEnabled", got.State)
	}
}

// TestTheSubsetRuleWaitsForTheResolutionLayersDeclaration holds the limit of the
// check: a host may supply the resolution capability itself, and a command with
// no resolution declaration has no list to compare against. The layer then
// starts and the host owns the pairing, as it owns every supplied instance.
func TestTheSubsetRuleWaitsForTheResolutionLayersDeclaration(t *testing.T) {
	got, err := prepare(readThrough(t, endpointsConfig(`["public"]`)))
	if err != nil {
		t.Fatalf("prepare over a supplied resolution with no declared list: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("the stance is %v, want StateEnabled", got.State)
	}
}

// TestResolveCarriesTheSSOExemptTenants holds the exemption list through the
// module's own resolution: the names reach the layer, and an empty name is
// refused like an empty endpoint name.
func TestResolveCarriesTheSSOExemptTenants(t *testing.T) {
	got, err := moduleConfig{
		Endpoints:        []string{"public"},
		SSOExemptTenants: []string{"system"},
	}.resolve()
	if err != nil {
		t.Fatalf("resolve refused a list of names: %v", err)
	}
	if len(got.ssoExemptTenants) != 1 || got.ssoExemptTenants[0] != "system" {
		t.Fatalf("resolve carried %v, want the tenant it was given", got.ssoExemptTenants)
	}

	if _, err := (moduleConfig{
		Endpoints:        []string{"public"},
		SSOExemptTenants: []string{""},
	}).resolve(); !errors.Is(err, authn.ErrInvalidConfig) {
		t.Fatalf("an empty exempt name answered %v, want authn.ErrInvalidConfig", err)
	}
}
