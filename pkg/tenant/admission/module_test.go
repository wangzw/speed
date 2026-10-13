package admission

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/ratelimit"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// TestTheDescriptorIsWhatTheDesignSays holds the descriptor field by field: the
// name, what it requires, what it delivers and that both deliveries are
// exclusive.
func TestTheDescriptorIsWhatTheDesignSays(t *testing.T) {
	m := Module()
	if m.Name != "tenant.admission" || ModuleName != "tenant.admission" || ConfigNamespace != ModuleName {
		t.Fatalf("the module is %q, the constant %q and the namespace %q, want tenant.admission for all", m.Name, ModuleName, ConfigNamespace)
	}
	wantRequires := []core.Requirement{
		{Token: (*authn.Memberships)(nil)},
		{Token: (*tenant.Resolution)(nil)},
		{Token: (*audit.Audit)(nil), Optional: true},
	}
	if !slices.Equal(m.Requires, wantRequires) {
		t.Fatalf("the module requires %+v, want the memberships and the resolution required and the audit capability optional", m.Requires)
	}
	wantProvides := []core.Provision{
		{Token: (*tenant.Admission)(nil), Exclusive: true},
		{Token: (*authn.ScopeGuard)(nil), Exclusive: true},
	}
	if !slices.Equal(m.Provides, wantProvides) {
		t.Fatalf("the module provides %+v, want the anchor and the guard, each exclusive", m.Provides)
	}
	if m.Name != ModuleName || m.Prepare == nil || m.New == nil || m.Init == nil {
		t.Fatal("a callback of the descriptor is missing")
	}
}

// TestPrepareStatesEnabledNeverAuto holds the stance a configured module takes
// through the real registry: StateAuto is the cell the resolution of an
// exclusive capability disables, and this module must fail that startup rather
// than give way.
func TestPrepareStatesEnabledNeverAuto(t *testing.T) {
	reader := readThrough(t, `{"tenant": {"admission": {"endpoints": ["public"]}}}`)
	got, err := prepare(reader)
	if err != nil {
		t.Fatalf("prepare over a complete configuration: %v", err)
	}
	if got.State != core.StateEnabled {
		t.Fatalf("the stance is %v, want StateEnabled", got.State)
	}
}

// TestTheRegistryDeliversBothCapabilitiesFromOneInstance drives the real
// registry to Init. The delivery check the registry runs on construction is
// the one that matters: a product that lacked a method of either interface
// fails the drive with ErrUndeliveredCapability.
func TestTheRegistryDeliversBothCapabilitiesFromOneInstance(t *testing.T) {
	err := fixture{}.drive(t, core.StageInit, func(_ context.Context, reg *core.Registry) error {
		anchor, err := core.Resolve[tenant.Admission](reg)
		if err != nil {
			t.Errorf("resolving the anchor: %v", err)
			return nil
		}
		guard, err := core.Resolve[authn.ScopeGuard](reg)
		if err != nil {
			t.Errorf("resolving the guard: %v", err)
			return nil
		}
		product, ok := anchor.(*layer)
		if !ok || any(guard) != any(product) {
			t.Errorf("the anchor is %T and the guard %T, want one instance of this module's product", anchor, guard)
			return nil
		}
		anchor.AdmissionLayer()
		if !guard.Covers("public") || guard.Covers("admin") {
			t.Error("the guard does not cover exactly the configured endpoint")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// otherPackage is a type of a package other than the tenant package, with the
// one method the anchor has.
type otherPackage struct{}

func (otherPackage) AdmissionLayer() {}

// stranger is a type of a package other than the rate limiter's.
type stranger struct{}

// TestTheMarkerMethodIsExportedSoAnotherPackageCanDeliverIt holds why the marker
// is exported. The interface is in the tenant package and its implementation in
// another one, so a method that only the tenant package could name would leave
// the one implementation undeliverable. The rate limiter's anchor, whose method
// is unexported and whose implementation is in the same package as the
// interface, is the case that shows the failure: a type of this package cannot
// deliver it.
func TestTheMarkerMethodIsExportedSoAnotherPackageCanDeliverIt(t *testing.T) {
	run := func(token core.Token, instance any) error {
		reg := core.New()
		reg.Register(core.Module{
			Name:     "stand-in",
			Provides: []core.Provision{{Token: token}},
			New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
		})
		return reg.Drive(t.Context(), core.Drive{Depth: core.StageInit, Participants: []string{"stand-in"}})
	}
	if err := run((*tenant.Admission)(nil), otherPackage{}); err != nil {
		t.Fatalf("a type of another package could not deliver the anchor: %v", err)
	}
	if err := run((*ratelimit.Limiter)(nil), stranger{}); !errors.Is(err, core.ErrUndeliveredCapability) {
		t.Fatalf("a type of another package delivered an interface with an unexported method, or failed otherwise: %v", err)
	}
}

// TestTwoAdmissionLayersFailTheStartup holds both exclusive claims.
func TestTwoAdmissionLayersFailTheStartup(t *testing.T) {
	for name, token := range map[string]core.Token{
		"the anchor": (*tenant.Admission)(nil),
		"the guard":  (*authn.ScopeGuard)(nil),
	} {
		t.Run(name, func(t *testing.T) {
			var instance any = otherPackage{}
			if token == core.Token((*authn.ScopeGuard)(nil)) {
				instance = guardStub{}
			}
			err := fixture{supplied: []supply{{"stand-in.other-layer", token, instance}}}.
				drive(t, core.StageInit, nil)
			if !errors.Is(err, core.ErrExclusiveViolated) {
				t.Fatalf("a second provider of %s ended the drive with %v, want ErrExclusiveViolated", name, err)
			}
		})
	}
}

type guardStub struct{}

func (guardStub) Covers(string) bool { return false }

// TestAMissingRequirementEndsAtDependencyResolution holds that neither
// capability the layer needs is optional.
func TestAMissingRequirementEndsAtDependencyResolution(t *testing.T) {
	for name, f := range map[string]fixture{
		"no memberships": {noMemberships: true},
		"no resolution":  {noResolution: true},
	} {
		t.Run(name, func(t *testing.T) {
			if err := f.drive(t, core.StageInit, nil); !errors.Is(err, core.ErrMissingProvider) {
				t.Fatalf("the drive ended with %v, want ErrMissingProvider", err)
			}
		})
	}
}

// TestInitFailures holds the sentinels Init reports.
func TestInitFailures(t *testing.T) {
	t.Run("no entry point is ErrNoRouter", func(t *testing.T) {
		err := fixture{noRouter: true}.drive(t, core.StageInit, nil)
		if !errors.Is(err, tenant.ErrNoRouter) {
			t.Fatalf("the drive ended with %v, want tenant.ErrNoRouter", err)
		}
		if !strings.Contains(err.Error(), diagnosticPrefix+ModuleName) {
			t.Fatalf("the failure does not name this module: %v", err)
		}
	})
	t.Run("an endpoint the entry point does not declare is ErrUnknownEndpoint and is named", func(t *testing.T) {
		body := `{
			"http": {"endpoints": {"public": {"address": "127.0.0.1:0"}}},
			"tenant": {"admission": {"endpoints": ["public", "typo"]}}
		}`
		err := fixture{body: body}.drive(t, core.StageInit, nil)
		if !errors.Is(err, tenant.ErrUnknownEndpoint) {
			t.Fatalf("the drive ended with %v, want tenant.ErrUnknownEndpoint", err)
		}
		if !strings.Contains(err.Error(), `"typo"`) {
			t.Fatalf("the failure does not name the endpoint: %v", err)
		}
	})
	t.Run("a configuration the module cannot run on ends the drive before Init", func(t *testing.T) {
		body := `{
			"http": {"endpoints": {"public": {"address": "127.0.0.1:0"}}},
			"tenant": {"admission": {"endpoints": ["*"]}}
		}`
		if err := (fixture{body: body}).drive(t, core.StageInit, nil); !errors.Is(err, authn.ErrInvalidConfig) {
			t.Fatalf("the drive ended with %v, want authn.ErrInvalidConfig", err)
		}
	})
}

// TestEveryOptionalCapabilityMayBeAbsent holds that a command with only what the
// layer requires starts.
func TestEveryOptionalCapabilityMayBeAbsent(t *testing.T) {
	if err := (fixture{}).drive(t, core.StageInit, nil); err != nil {
		t.Fatalf("the drive with no optional capability failed: %v", err)
	}
}

// TestEveryOptionalCapabilityIsTakenUp holds that each capability a case supplies
// is the one the product holds after Init.
func TestEveryOptionalCapabilityIsTakenUp(t *testing.T) {
	providers := standardProviders()
	decider := &deciderStub{}
	store := newSettingsStub()
	logs := newRecorder()
	bus := newBusStub()
	f := fixture{supplied: []supply{
		withProviders(providers), withDecider(decider), withStore(store), withLogs(logs), withBus(bus),
	}}
	err := f.drive(t, core.StageInit, func(_ context.Context, reg *core.Registry) error {
		anchor, err := core.Resolve[tenant.Admission](reg)
		if err != nil {
			return err
		}
		l := anchor.(*layer)
		if l.providers != authn.SSOProviders(providers) || l.decider != authn.Decider(decider) || l.store == nil {
			t.Errorf("the product holds providers %v, decider %v, store %v", l.providers, l.decider, l.store)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// TestAnOptionalCapabilityThatFailsToResolveStopsTheStartup holds the other half
// of optional(): only ErrMissingProvider leaves the capability out, and any
// other failure the lookup reports is carried out and ends the startup. A
// capability the lookup cannot resolve for another reason must not be dropped
// silently, which is what leaving the layer with the capability absent would do.
func TestAnOptionalCapabilityThatFailsToResolveStopsTheStartup(t *testing.T) {
	t.Run("a capability with two providers stops the startup", func(t *testing.T) {
		err := fixture{supplied: []supply{
			{"stand-in.settings-one", (*settings.Store)(nil), newSettingsStub()},
			{"stand-in.settings-two", (*settings.Store)(nil), newSettingsStub()},
		}}.drive(t, core.StageInit, nil)
		if !errors.Is(err, core.ErrAmbiguousProvider) {
			t.Fatalf("the drive ended with %v, want core.ErrAmbiguousProvider: a capability that fails to resolve must stop the startup, not be left out", err)
		}
	})
	t.Run("the entry point reports its own failure, not ErrNoRouter", func(t *testing.T) {
		err := fixture{
			noRouter: true,
			body:     `{"tenant": {"admission": {"endpoints": ["public"]}}}`,
			supplied: []supply{
				{"stand-in.router-one", (*speedhttp.Router)(nil), routerStub{}},
				{"stand-in.router-two", (*speedhttp.Router)(nil), routerStub{}},
			},
		}.drive(t, core.StageInit, nil)
		if !errors.Is(err, core.ErrAmbiguousProvider) {
			t.Fatalf("the drive ended with %v, want core.ErrAmbiguousProvider", err)
		}
		if errors.Is(err, tenant.ErrNoRouter) {
			t.Fatalf("an entry point that failed to resolve was reported as ErrNoRouter: %v", err)
		}
	})
}

// routerStub stands for an entry point module: only the capability, which is
// what makes it something Resolve counts when two of them are supplied.
type routerStub struct{}

func (routerStub) Endpoint(string) (speedhttp.Endpoint, error) { return nil, nil }
func (routerStub) Endpoints() []string                         { return nil }

// TestASuppliedInstanceThatIsNotTheProductIsLeftAlone holds the design's
// non-guarantee about a host's own instance: the callbacks do nothing to it, so
// a command with no entry point starts, which a product could not.
func TestASuppliedInstanceThatIsNotTheProductIsLeftAlone(t *testing.T) {
	err := fixture{noRouter: true, instance: &hostAdmission{}}.drive(t, core.StageInit, nil)
	if err != nil {
		t.Fatalf("the drive over a host's own instance failed: %v", err)
	}
}

// TestACallbackActsOnTheProductAndNothingElse holds the same property from the
// other side: a wrapper carrying the product's methods is no product.
func TestACallbackActsOnTheProductAndNothingElse(t *testing.T) {
	product := newLayer(resolved{endpoints: []string{"public"}}, newMemberStub())
	wrapper := &wrapped{layer: product}
	if _, ok := productOf(wrapper); ok {
		t.Fatal("a wrapper around the product was taken for the product")
	}
	if got, ok := productOf(product); !ok || got != product {
		t.Fatal("the product was not recognised")
	}
}

type hostAdmission struct{}

func (*hostAdmission) AdmissionLayer()    {}
func (*hostAdmission) Covers(string) bool { return true }

type wrapped struct{ *layer }

// TestTheMiddlewareDeclaresWhatTheDesignSays pins the registration: the name,
// the anchor it stands for and the stage it declares. The chain cases in
// live_test.go hold what the entry point makes of it; this holds the
// declaration itself.
func TestTheMiddlewareDeclaresWhatTheDesignSays(t *testing.T) {
	mw := newLayer(resolved{endpoints: []string{"public"}}, newMemberStub()).middlewareFor("public")
	if mw.Name != "tenant.admission" || mw.Stage != speedhttp.StageAuthorize || mw.Handler == nil {
		t.Fatalf("the layer is %q in stage %v, want tenant.admission in the authorizing stage", mw.Name, mw.Stage)
	}
	if !slices.Equal(mw.Provides, []core.Token{(*tenant.Admission)(nil)}) {
		t.Fatalf("the layer stands for %v, want the admission anchor alone", mw.Provides)
	}
	if len(mw.After) != 0 {
		t.Fatalf("the layer declares the constraints %v, and the stage order supplies its position", mw.After)
	}
}
