package core

import (
	"errors"
	"reflect"
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

func allEnabled(mods ...Module) map[string]Enablement {
	final := make(map[string]Enablement, len(mods))
	for _, m := range mods {
		final[m.Name] = Enablement{State: StateEnabled}
	}
	return final
}

func mustPlan(t *testing.T, mods ...Module) []string {
	t.Helper()
	order, err := planConstruction(mods, mods, nil, nil, allEnabled(mods...), nil, nil)
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

func TestMissingProviderNoDeclarer(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, nil, allEnabled(consumer), nil, nil)
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

// TestFixingActionsListsWhatCanBeDone pins the actions a capability nothing
// delivers leaves a host with: import an implementation, and -- once the
// requiring module is named -- disable it. "Enable one that is already
// imported" is not among them, because this failure is reached only when no
// registered module declared the capability, so an implementation already
// imported does not exist by construction.
//
// The single-action shape has to read as a sentence on its own: the Resolve
// path reaches the failure with no requiring module to name, and joining one
// action as though there were a list would leave a dangling ", or".
func TestFixingActionsListsWhatCanBeDone(t *testing.T) {
	if got, want := fixingActions(""), "Import an implementation of it"; got != want {
		t.Errorf("with no requiring module, fixingActions is %q, want %q", got, want)
	}
	if got, want := fixingActions("api"), `Import an implementation of it, or disable "api"`; got != want {
		t.Errorf("with a requiring module, fixingActions is %q, want %q", got, want)
	}
}

// TestMissingProviderForAnExclusiveCapabilitySaysToNameOne pins the fix that
// belongs to the closure's exception. A capability its providers claim
// exclusively is not pulled into a command, so a consumer of it fails with an
// instruction to name one implementation -- not with the instruction to import
// one, which is already satisfied and would change nothing.
func TestMissingProviderForAnExclusiveCapabilitySaysToNameOne(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	memory := Module{Name: "memory", Provides: []Provision{exclusive((*cacheCap)(nil))}}
	redis := Module{Name: "redis", Provides: []Provision{exclusive((*cacheCap)(nil))}}

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil,
		[]Module{memory, redis}, allEnabled(consumer), nil, nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	for _, want := range []string{"memory", "redis", "claimed exclusively", "name one implementation"} {
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

// TestMissingProviderTextCarriesDisableReason pins that a capability lost to
// resolution reports why its provider is not running, not merely that nobody
// provides it.
func TestMissingProviderTextCarriesDisableReason(t *testing.T) {
	provider := Module{Name: "provider", Provides: provides((*cacheCap)(nil))}
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	final := map[string]Enablement{
		"consumer": {State: StateEnabled},
		"provider": {State: StateDisabled, Reason: "no connection address configured"},
	}

	_, err := planConstruction([]Module{consumer}, []Module{consumer, provider}, nil, nil, final, nil, nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	for _, want := range []string{"provider", "no connection address configured"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestMissingProviderNamesTheRequiringModule pins that the failure says which
// module asked for the capability. The available set is decided by imports, so
// the module left unsatisfied may be one a dependency pulled in rather than one
// the host wrote down, and disabling it is a fixing action no host can take
// without its name.
func TestMissingProviderNamesTheRequiringModule(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, nil, allEnabled(consumer), nil, nil)
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("planning returned %v, want ErrMissingProvider", err)
	}
	if !strings.Contains(err.Error(), `"consumer"`) {
		t.Errorf("error %q does not name the module whose requirement went unmet, so a host "+
			"reading it cannot tell which module to satisfy or to disable", err)
	}
}

func TestOptionalMissingIsNotAnError(t *testing.T) {
	consumer := Module{
		Name:     "consumer",
		Requires: []Requirement{{Token: (*cacheCap)(nil), Optional: true}},
	}

	order, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, nil, allEnabled(consumer), nil, nil)
	if err != nil {
		t.Fatalf("an absent optional dependency failed the planning: %v", err)
	}
	if !slices.Equal(order, []string{"consumer"}) {
		t.Fatalf("order is %v, want just the consumer", order)
	}
}

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

func TestCycleDetectedNamingModules(t *testing.T) {
	a := Module{Name: "a", Requires: needs((*storeCap)(nil)), Provides: provides((*cacheCap)(nil))}
	b := Module{Name: "b", Requires: needs((*cacheCap)(nil)), Provides: provides((*storeCap)(nil))}

	_, err := planConstruction([]Module{a, b}, []Module{a, b}, nil, nil, allEnabled(a, b), nil, nil)
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

func TestSelfCycle(t *testing.T) {
	m := Module{
		Name:     "self",
		Requires: needs((*cacheCap)(nil)),
		Provides: provides((*cacheCap)(nil)),
	}

	_, err := planConstruction([]Module{m}, []Module{m}, nil, nil, allEnabled(m), nil, nil)
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("planning returned %v, want ErrDependencyCycle", err)
	}
	if !strings.Contains(err.Error(), "self") {
		t.Errorf("error %q does not name the module", err)
	}
}

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

func TestOrderStableAcrossRegistrationOrders(t *testing.T) {
	a := Module{Name: "alpha", Provides: provides((*cacheCap)(nil))}
	b := Module{Name: "bravo", Requires: needs((*cacheCap)(nil)), Provides: provides((*storeCap)(nil))}
	c := Module{Name: "charlie", Requires: needs((*storeCap)(nil))}
	// delta shares bravo's tier: nothing orders the two, so the tie is
	// broken by name and must not follow registration order.
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
	// Each step takes the smallest name among those whose dependencies are
	// already placed: alpha alone starts ready, placing it readies bravo and
	// delta, and bravo wins that tie by name and readies charlie, which then
	// wins against delta.
	if !slices.Equal(want, []string{"alpha", "bravo", "charlie", "delta"}) {
		t.Fatalf("order is %v, want ties broken by name at each step", want)
	}
}

// TestSuppliedCapabilityMeetsARequirementWithoutAnEdge pins that a supplied
// product satisfies a requirement and orders nothing: it exists before the
// first construction, and an edge would point at a node the graph has no
// entry for.
func TestSuppliedCapabilityMeetsARequirementWithoutAnEdge(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*readerCap)(nil))}
	supplied := map[reflect.Type]bool{
		capabilityType((*readerCap)(nil), "test"): true,
	}

	order, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, nil,
		allEnabled(consumer), nil, supplied)
	if err != nil {
		t.Fatalf("a requirement a supplied product meets failed the planning: %v", err)
	}
	if !slices.Equal(order, []string{"consumer"}) {
		t.Fatalf("order is %v, want just the consumer", order)
	}
}

// TestMissingProviderNamesAModuleThisCommandLeftOut pins the fourth cause
// apart from the other three. A provider outside the participating set is
// neither disabled nor ordered late, so reporting it as either gives an
// instruction that changes nothing; the fix is to include it.
func TestMissingProviderNamesAModuleThisCommandLeftOut(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	provider := Module{Name: "left-out", Provides: provides((*cacheCap)(nil))}

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, nil, []Module{provider},
		allEnabled(consumer), nil, nil)
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

// TestMissingProviderTellsADeclarationLevelModuleApart pins the fifth cause
// apart from the fourth.
//
// A module taken in for its declarations alone is taken in, so the advice the
// fourth cause gives -- add it to the command's Include -- is false of it, and
// following that advice constructs a module the command was written not to
// build. What it cannot do is deliver: it is never constructed, however much it
// declares. The two groups arrive from the same command's writing and lead to
// opposite fixes, so a report that merged them would send half its readers the
// wrong way.
func TestMissingProviderTellsADeclarationLevelModuleApart(t *testing.T) {
	consumer := Module{Name: "consumer", Requires: needs((*cacheCap)(nil))}
	declarer := Module{Name: "collected", Provides: provides((*cacheCap)(nil))}

	_, err := planConstruction([]Module{consumer}, []Module{consumer}, []Module{declarer}, nil,
		allEnabled(consumer), nil, nil)
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

// TestTheDriveReachesTheDeclarationLevelInItsReport is the same report through
// a drive rather than through the planner, so that the split a drive makes is
// the split the report is built from.
//
// Nothing in the planner can tell the two groups of outside modules apart: it
// is given them already divided, and this is what checks that the drive divides
// them the way it says it does.
func TestTheDriveReachesTheDeclarationLevelInItsReport(t *testing.T) {
	reg := New()
	reg.Register(Module{Name: "consumer", Requires: needs((*cacheCap)(nil))})
	reg.Register(Module{Name: "collected", Provides: provides((*cacheCap)(nil))})

	// StageNew rather than the zero depth: a command that collects stances
	// alone never resolves dependencies, and this is about the report a
	// construction attempt makes.
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
