package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func needs(tokens ...Token) []Requirement {
	out := make([]Requirement, 0, len(tokens))
	for _, tok := range tokens {
		out = append(out, Requirement{Token: tok})
	}
	return out
}

func building(name string, provs ...Provision) Module {
	return Module{
		Name:     name,
		Provides: provs,
		New:      func(context.Context, *Registry) (any, error) { return &product{id: name}, nil },
	}
}

func allEnabled(mods ...Module) map[string]Enablement {
	final := make(map[string]Enablement, len(mods))
	for _, m := range mods {
		final[m.Name] = Enablement{State: StateEnabled}
	}
	return final
}

func mustPlan(t *testing.T, mods ...Module) []string {
	t.Helper()
	order, err := planConstruction(mods, mods, nil, nil, allEnabled(mods...), nil)
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	return order
}

func indexOf(t *testing.T, order []string, name string) int {
	t.Helper()
	i := slices.Index(order, name)
	if i < 0 {
		t.Fatalf("module %q is missing from the order %v", name, order)
	}
	return i
}

// TestMissingProviderNoDeclarer checks that a requirement no module delivers fails as a missing
// provider, naming the no-declarer case and a fixing action.
func TestMissingProviderNoDeclarer(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, nil, allEnabled(consumer), nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	if !strings.Contains(err.Error(), "no module delivers") {
		t.Errorf("error %q does not read as the no-declarer case", err)
	}
	if !strings.Contains(err.Error(), "Import") {
		t.Errorf("error %q does not state a fixing action", err)
	}
}

// TestFixingActionsListsWhatCanBeDone pins the actions a capability nothing delivers leaves a host
// with: import an implementation, and -- once the requiring module is named -- disable it.
func TestFixingActionsListsWhatCanBeDone(t *testing.T) {
	if got, want := fixingActions("", false), "Import an implementation of it"; got != want {
		t.Errorf("with no requiring module, fixingActions is %q, want %q", got, want)
	}
	if got, want := fixingActions("api", false), `Import an implementation of it, or leave "api" out of this command`; got != want {
		t.Errorf("with a requiring module, fixingActions is %q, want %q", got, want)
	}
}

// TestFixingActionsForARequirerWithASuppliedInstance pins the actions that differ when the module
// whose requirement went unmet has a supplied instance.
func TestFixingActionsForARequirerWithASuppliedInstance(t *testing.T) {
	got := fixingActions("api", true)
	for _, want := range []string{
		"Import an implementation of it",
		"take it in on the command tree, or hand an instance of it in with Supply",
		"not expanded",
		`stop supplying an instance to "api"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fixingActions for a supplied requirer is %q, which does not say %q", got, want)
		}
	}
	if strings.Contains(got, "out of this command") {
		t.Errorf("fixingActions for a supplied requirer is %q, which offers to leave it out of a "+
			"command it is in whatever the command tree says", got)
	}
}

// TestMissingProviderForAnExclusiveCapabilitySaysToNameOne pins the fix that belongs to the
// closure's exception.
func TestMissingProviderForAnExclusiveCapabilitySaysToNameOne(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	memory := building("memory", exclusive((*cacheCap)(nil)))
	redis := building("redis", exclusive((*cacheCap)(nil)))

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil,
		[]Module{memory, redis}, allEnabled(consumer), nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	for _, want := range []string{
		"memory", "redis", "claimed exclusively", "name one implementation",
		"hand an instance of one of them in with Supply",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "declares it but this command does not include it") {
		t.Errorf("error %q gives the import-and-include fix for a provider that is registered "+
			"and specifically left out by the expansion rule, which sends the reader to a "+
			"change that does nothing", err)
	}
}

// TestMissingProviderTextCarriesDisableReason pins that a capability lost to resolution reports
// why its provider is not running, not merely that nobody provides it.
func TestMissingProviderTextCarriesDisableReason(t *testing.T) {
	provider := building("provider", provides((*cacheCap)(nil))...)
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	final := map[string]Enablement{
		"consumer": {State: StateEnabled},
		"provider": {State: StateDisabled, Reason: "no connection address configured"},
	}

	_, err := planConstruction([]Module{consumer}, []Module{consumer, provider}, nil, nil, final, nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	for _, want := range []string{"provider", "no connection address configured"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestMissingProviderNamesTheRequiringModule pins that the failure says which module asked for the
// capability.
func TestMissingProviderNamesTheRequiringModule(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, nil, allEnabled(consumer), nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	if !strings.Contains(err.Error(), `"consumer"`) {
		t.Errorf("error %q does not name the module whose requirement went unmet, so a host "+
			"reading it cannot tell which module to satisfy or to disable", err)
	}
}

// TestOptionalMissingIsNotAnError checks that an absent optional requirement neither fails
// planning nor enters the order.
func TestOptionalMissingIsNotAnError(t *testing.T) {
	consumer := Module{
		Name:     "consumer",
		Requires: []Requirement{{Token: (*cacheCap)(nil), Optional: true}},
	}

	order, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, nil, allEnabled(consumer), nil)
	if err != nil {
		t.Fatalf("an absent optional dependency failed the planning: %v", err)
	}
	if !slices.Equal(order, []string{"consumer"}) {
		t.Fatalf("order is %v, want just the consumer", order)
	}
}

// TestOptionalPresentStillOrders checks that a present optional provider is placed before its
// consumer.
func TestOptionalPresentStillOrders(t *testing.T) {
	provider := Module{Name: "zzz-provider", Provides: provides((*cacheCap)(nil))}
	consumer := Module{
		Name:     "aaa-consumer",
		Requires: []Requirement{{Token: (*cacheCap)(nil), Optional: true}},
	}

	order := mustPlan(t, consumer, provider)
	if indexOf(t, order, "zzz-provider") > indexOf(t, order, "aaa-consumer") {
		t.Fatalf("order is %v, want the present optional provider first", order)
	}
}

// TestCycleDetectedNamingModules checks that a dependency cycle fails as ErrDependencyCycle,
// naming every module on it and the way out.
func TestCycleDetectedNamingModules(t *testing.T) {
	a := Module{Name: "a", Requires: needs((*storeCap)(nil)), Provides: provides((*cacheCap)(nil))}
	b := Module{Name: "b", Requires: needs((*cacheCap)(nil)), Provides: provides((*storeCap)(nil))}

	_, err := planConstruction([]Module{a, b}, []Module{a, b}, nil, nil, allEnabled(a, b), nil)
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("planning returned %v, want ErrDependencyCycle", err)
	}
	for _, name := range []string{"a", "b"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
	if !strings.Contains(err.Error(), "Init") {
		t.Errorf("error %q does not point at the way out", err)
	}
}

// TestSelfCycle checks that a module requiring its own capability fails as a
// dependency cycle naming the module.
func TestSelfCycle(t *testing.T) {
	m := Module{
		Name:     "self",
		Requires: needs((*cacheCap)(nil)),
		Provides: provides((*cacheCap)(nil)),
	}

	_, err := planConstruction([]Module{m}, []Module{m}, nil, nil, allEnabled(m), nil)
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("planning returned %v, want ErrDependencyCycle", err)
	}
	if !strings.Contains(err.Error(), "self") {
		t.Errorf("error %q does not name the module", err)
	}
}

// TestConsumerFollowsAllProviders checks that a requirement with several providers is placed after
// all of them.
func TestConsumerFollowsAllProviders(t *testing.T) {
	one := Module{Name: "one", Provides: provides((*storeCap)(nil))}
	two := Module{Name: "two", Provides: provides((*storeCap)(nil))}
	consumer := Module{Name: "a-consumer", Requires: needs((*storeCap)(nil))}

	order := mustPlan(t, consumer, one, two)
	at := indexOf(t, order, "a-consumer")
	if indexOf(t, order, "one") > at || indexOf(t, order, "two") > at {
		t.Fatalf("order is %v, want the consumer after both providers", order)
	}
}

// TestOrderStableAcrossRegistrationOrders checks that the construction order, ties broken by name,
// does not depend on registration order.
func TestOrderStableAcrossRegistrationOrders(t *testing.T) {
	a := Module{Name: "alpha", Provides: provides((*cacheCap)(nil))}
	b := Module{Name: "bravo", Requires: needs((*cacheCap)(nil)), Provides: provides((*storeCap)(nil))}
	c := Module{Name: "charlie", Requires: needs((*storeCap)(nil))}
	d := Module{Name: "delta", Requires: needs((*cacheCap)(nil))}

	want := mustPlan(t, a, b, c, d)
	for _, permuted := range [][]Module{
		{d, c, b, a},
		{b, d, a, c},
		{c, a, d, b},
	} {
		got := mustPlan(t, permuted...)
		if !slices.Equal(got, want) {
			t.Fatalf("registration order changed the construction order: got %v, want %v", got, want)
		}
	}
	if !slices.Equal(want, []string{"alpha", "bravo", "charlie", "delta"}) {
		t.Fatalf("order is %v, want ties broken by name at each step", want)
	}
}

// TestAModuleWithoutNewIsAProviderLikeAnyOther pins that planning does not look at where an
// instance comes from.
func TestAModuleWithoutNewIsAProviderLikeAnyOther(t *testing.T) {
	provider := Module{Name: "zzz-supplied", Provides: provides((*readerCap)(nil))}
	consumer := Module{Name: "aaa-consumer", Requires: needs((*readerCap)(nil))}

	order := mustPlan(t, consumer, provider)
	if !slices.Equal(order, []string{"zzz-supplied", "aaa-consumer"}) {
		t.Fatalf("order is %v, want the module the consumer requires placed first although it "+
			"sorts last by name", order)
	}
}

// TestMissingProviderNamesAModuleThisCommandLeftOut pins the fourth cause apart from the other
// three.
func TestMissingProviderNamesAModuleThisCommandLeftOut(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	provider := building("left-out", provides((*cacheCap)(nil))...)

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, []Module{provider},
		allEnabled(consumer), nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	if !strings.Contains(err.Error(), "left-out") {
		t.Errorf("error %q does not name the provider this command left out", err)
	}
	if !strings.Contains(err.Error(), "Include") {
		t.Errorf("error %q does not point at the fix, which is to include that module", err)
	}
	for _, wrong := range []string{"constructed later", "is not enabled"} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("error %q reports the module as %q, which is an instruction that "+
				"changes nothing: it is not in this command at all", err, wrong)
		}
	}
}

// TestMissingProviderTellsADeclarationLevelModuleApart pins the fifth cause apart from the fourth.
func TestMissingProviderTellsADeclarationLevelModuleApart(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	declarer := building("collected", provides((*cacheCap)(nil))...)

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, []Module{declarer}, nil,
		allEnabled(consumer), nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	if !strings.Contains(err.Error(), "collected") {
		t.Errorf("error %q does not name the provider this command collects from", err)
	}
	if !strings.Contains(err.Error(), "declarations alone") {
		t.Errorf("error %q does not say the module was taken in for its declarations alone, "+
			"which is the one thing that tells this cause from the ones beside it", err)
	}
	if strings.Contains(err.Error(), "does not include it") {
		t.Errorf("error %q says the command does not include a module it did take in", err)
	}
}

// TestTheDriveReachesTheDeclarationLevelInItsReport is the same report through a drive rather than
// through the planner, so that the split a drive makes is the split the report is built from.
func TestTheDriveReachesTheDeclarationLevelInItsReport(t *testing.T) {
	reg := New()
	reg.Register(Module{Name: "consumer", Requires: needs((*cacheCap)(nil))})
	reg.Register(building("collected", provides((*cacheCap)(nil))...))

	err := reg.Drive(t.Context(), Drive{
		Depth:         StageNew,
		Participants:  []string{"consumer"},
		DeclaringOnly: []Declaration{{Name: "collected"}},
	})
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("Drive returned %v, want ErrMissingProvider", err)
	}
	if !strings.Contains(err.Error(), "declarations alone") {
		t.Errorf("the drive's report reads %q, which does not say the module was reached for "+
			"its declarations alone", err)
	}
	if strings.Contains(err.Error(), "does not include it") {
		t.Errorf("the drive's report reads %q, which says of a module it took in that it "+
			"did not", err)
	}
}

// TestMissingProviderForAModuleWithoutNewPointsAtSupply pins the cause that outranks the ones
// beside it.
func TestMissingProviderForAModuleWithoutNewPointsAtSupply(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	hostOnly := Module{Name: "host-only", Provides: provides((*cacheCap)(nil))}
	hostOnlyExclusive := Module{Name: "host-only", Provides: []Provision{exclusive((*cacheCap)(nil))}}

	for name, plan := range map[string]func() error{
		"left out of the command": func() error {
			_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil,
				[]Module{hostOnly}, allEnabled(consumer), nil)
			return err
		},
		"taken in for its declarations alone": func() error {
			_, err := planConstruction([]Module{consumer}, []Module{consumer}, []Module{hostOnly},
				nil, allEnabled(consumer), nil)
			return err
		},
		"left out because it claims the capability exclusively": func() error {
			_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil,
				[]Module{hostOnlyExclusive}, allEnabled(consumer), nil)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := plan()
			if !errors.Is(err, ErrMissingProvider) {
				t.Fatalf("planning returned %v, want ErrMissingProvider", err)
			}
			for _, want := range []string{`"host-only"`, "no New callback", "supplied no instance", "Supply"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			for _, wrong := range []string{
				"does not include it", "Include", "declarations alone",
				"claimed exclusively", "name one implementation", "constructed later",
			} {
				if strings.Contains(err.Error(), wrong) {
					t.Errorf("error %q carries %q, the fix of a cause that is not this module's: "+
						"nothing but supplying an instance gives it one", err, wrong)
				}
			}
		})
	}
}

// TestMissingProviderGivesEachDeclarerItsOwnFix pins that the report is made per declarer.
func TestMissingProviderGivesEachDeclarerItsOwnFix(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	builds := building("builds", provides((*cacheCap)(nil))...)
	supplied := Module{Name: "supplied-only", Provides: provides((*cacheCap)(nil))}

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil,
		[]Module{builds, supplied}, allEnabled(consumer), nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	for _, want := range []string{
		`"builds" declares it but this command does not include it`,
		`"supplied-only" declares it but has no New callback`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
	for _, wrong := range []string{
		`"supplied-only" declares it but this command does not include it`,
		`"builds" declares it but has no New callback`,
	} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("error %q says %q, which gives a module the other one's fix", err, wrong)
		}
	}
}

// TestMissingProviderKeepsTheDisableReasonForAModuleWithoutNew pins the exception to the rule
// above.
func TestMissingProviderKeepsTheDisableReasonForAModuleWithoutNew(t *testing.T) {
	provider := Module{Name: "provider", Provides: provides((*cacheCap)(nil))}
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	final := map[string]Enablement{
		"consumer": {State: StateEnabled},
		"provider": {State: StateDisabled, Reason: "stood down for another provider"},
	}

	_, err := planConstruction([]Module{consumer}, []Module{consumer, provider}, nil, nil, final, nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	if !strings.Contains(err.Error(), "stood down for another provider") {
		t.Errorf("error %q does not carry the reason the provider is disabled", err)
	}
	if strings.Contains(err.Error(), "no New callback") {
		t.Errorf("error %q reports a disabled module as one the host failed to supply", err)
	}
}

// TestMissingProviderNamesWhenTheRequirerHasASuppliedInstance checks that a missing provider's
// message distinguishes a supplied requirer, a left-out provider and a named one.
func TestMissingProviderNamesWhenTheRequirerHasASuppliedInstance(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	origins := map[string]origin{"consumer": {site: "example.com/host", supplied: true}}

	t.Run("nothing delivers the capability", func(t *testing.T) {
		_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, nil,
			allEnabled(consumer), origins)
		if !errors.Is(err, ErrMissingProvider) {
			t.Fatalf("planning returned %v, want ErrMissingProvider", err)
		}
		for _, want := range []string{
			"registered from example.com/host", "has a supplied instance",
			`stop supplying an instance to "consumer"`,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "out of this command") {
			t.Errorf("error %q offers to leave out of the command a module the host supplied", err)
		}
	})

	t.Run("a provider is registered and not in the command", func(t *testing.T) {
		provider := building("left-out", provides((*cacheCap)(nil))...)
		_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, []Module{provider},
			allEnabled(consumer), origins)
		if !errors.Is(err, ErrMissingProvider) {
			t.Fatalf("planning returned %v, want ErrMissingProvider", err)
		}
		for _, want := range []string{
			`"left-out" declares it but this command does not include it`,
			"hand an instance of it in with Supply", "not expanded",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "the command that needs it") {
			t.Errorf("error %q sends the reader to a command that does not need the supplied "+
				"module: it is there because the host supplied it", err)
		}
	})

	t.Run("the requirer was not supplied", func(t *testing.T) {
		provider := building("left-out", provides((*cacheCap)(nil))...)
		_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, []Module{provider},
			allEnabled(consumer), map[string]origin{"consumer": {site: "example.com/host"}})
		if !errors.Is(err, ErrMissingProvider) {
			t.Fatalf("planning returned %v, want ErrMissingProvider", err)
		}
		if !strings.Contains(err.Error(), "the command that needs it") {
			t.Errorf("error %q lost the fix that belongs to a requirer the command tree named", err)
		}
		for _, wrong := range []string{"supplied", "not expanded"} {
			if strings.Contains(err.Error(), wrong) {
				t.Errorf("error %q says %q of a module nobody supplied", err, wrong)
			}
		}
	})
}

// TestDependencyOrderPlacesProvidersBeforeTheirRequirers checks that each provider precedes its
// requirer whatever order the modules are given in.
func TestDependencyOrderPlacesProvidersBeforeTheirRequirers(t *testing.T) {
	a := Module{Name: "alpha", Requires: needs((*cacheCap)(nil))}
	b := Module{Name: "bravo", Requires: needs((*storeCap)(nil)), Provides: provides((*cacheCap)(nil))}
	c := Module{Name: "charlie", Provides: provides((*storeCap)(nil))}

	want := []string{"charlie", "bravo", "alpha"}
	for _, mods := range [][]Module{{a, b, c}, {c, b, a}, {b, c, a}} {
		if got := dependencyOrder(mods); !slices.Equal(got, want) {
			t.Fatalf("dependencyOrder is %v, want %v whatever order the modules are given in", got, want)
		}
	}
}

// TestDependencyOrderValidatesNothing pins that the order is computed over the modules given and
// no others.
func TestDependencyOrderValidatesNothing(t *testing.T) {
	needsOutsider := Module{Name: "needs-outsider", Requires: needs((*readerCap)(nil))}
	optional := Module{Name: "optional", Requires: []Requirement{{Token: (*cacheCap)(nil), Optional: true}}}

	got := dependencyOrder([]Module{optional, needsOutsider})
	if !slices.Equal(got, []string{"needs-outsider", "optional"}) {
		t.Fatalf("dependencyOrder is %v, want both, by name, with the unmet requirements ignored", got)
	}
}

// TestDependencyOrderKeepsTheModulesOnACycle pins that a cycle does not drop its members.
func TestDependencyOrderKeepsTheModulesOnACycle(t *testing.T) {
	root := Module{Name: "zz-root", Provides: provides((*readerCap)(nil))}
	x := Module{Name: "x", Requires: needs((*storeCap)(nil)), Provides: provides((*cacheCap)(nil))}
	y := Module{Name: "y", Requires: needs((*cacheCap)(nil)), Provides: provides((*storeCap)(nil))}
	behind := Module{Name: "behind", Requires: needs((*cacheCap)(nil), (*readerCap)(nil))}

	got := dependencyOrder([]Module{behind, y, x, root})
	if want := []string{"zz-root", "behind", "x", "y"}; !slices.Equal(got, want) {
		t.Fatalf("dependencyOrder is %v, want %v: the root placed, then the rest by name", got, want)
	}
}
