package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func seed(t *testing.T, r *Registry, m Module, instance any) {
	t.Helper()
	r.Register(m)
	if instance != nil {
		r.instances[m.Name] = instance
	}
}

func provides(tokens ...Token) []Provision {
	out := make([]Provision, 0, len(tokens))
	for _, tok := range tokens {
		out = append(out, Provision{Token: tok})
	}
	return out
}

// TestResolveIgnoresUndeclaredImplementation pins the declaration as the sole authority: a product
// that happens to implement a capability its module never declared is invisible.
func TestResolveIgnoresUndeclaredImplementation(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "undeclared"}, &product{id: "undeclared"})

	_, err := reg.Resolve((*cacheCap)(nil))
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("Resolve returned %v, want ErrMissingProvider", err)
	}
}

// TestResolveAmbiguous checks that a capability two modules deliver fails as ErrAmbiguousProvider
// naming both.
func TestResolveAmbiguous(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "one", Provides: provides((*cacheCap)(nil))}, &product{id: "one"})
	seed(t, reg, Module{Name: "two", Provides: provides((*cacheCap)(nil))}, &product{id: "two"})

	_, err := reg.Resolve((*cacheCap)(nil))
	if !errors.Is(err, ErrAmbiguousProvider) {
		t.Fatalf("Resolve returned %v, want ErrAmbiguousProvider", err)
	}
	for _, name := range []string{"one", "two"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name provider %s", err, name)
		}
	}
}

// TestResolveDoesNotFilterByRequires checks that Resolve returns a constructed instance whose
// module never declared the capability.
func TestResolveDoesNotFilterByRequires(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "c"}, &product{id: "c"})
	seed(t, reg, Module{Name: "d", Provides: provides((*storeCap)(nil))}, &product{id: "d"})

	got, err := reg.Resolve((*storeCap)(nil))
	if err != nil {
		t.Fatalf("Resolve failed for an undeclared but constructed capability: %v", err)
	}
	if got.(storeCap).storeID() != "d" {
		t.Fatalf("Resolve returned %v, want d's product", got)
	}
}

// TestResolveNamesLaterConstructedProvider separates "constructed later" from "nobody delivers
// it": the fixing actions differ.
func TestResolveNamesLaterConstructedProvider(t *testing.T) {
	reg := New()
	var err error
	reg.Register(Module{
		Name: "early",
		New: func(_ context.Context, r *Registry) (any, error) {
			_, err = r.Resolve((*cacheCap)(nil))
			return nil, nil
		},
	})
	reg.Register(Module{
		Name:     "later",
		Provides: provides((*cacheCap)(nil)),
		New:      func(context.Context, *Registry) (any, error) { return &product{id: "later"}, nil },
	})
	if drove := reg.Drive(t.Context(), Drive{Depth: StageNew, Participants: []string{"early", "later"}}); drove != nil {
		t.Fatalf("the drive failed: %v", drove)
	}
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("Resolve returned %v, want ErrMissingProvider", err)
	}
	for _, want := range []string{"later", "Requires", "Init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	other := New()
	_, absent := other.Resolve((*cacheCap)(nil))
	if !errors.Is(absent, ErrMissingProvider) {
		t.Fatalf("Resolve returned %v, want ErrMissingProvider", absent)
	}
	if strings.Contains(absent.Error(), "Requires") {
		t.Errorf("the no-declarer message %q reads like the constructed-later one", absent)
	}
}

// TestResolveBeforeAnyProductExistsSaysSo checks that a missing provider before construction, in
// Prepare or before the action is reported with that context, not as a construction-order problem.
func TestResolveBeforeAnyProductExistsSaysSo(t *testing.T) {
	provider := Module{
		Name:     "provider",
		Provides: provides((*cacheCap)(nil)),
		New:      func(context.Context, *Registry) (any, error) { return &product{id: "p"}, nil },
	}
	cases := map[string]struct {
		lookup   func(t *testing.T) error
		says     string
		supplied string
	}{
		"before the drive": {
			lookup: func(t *testing.T) error {
				reg := New()
				reg.Register(provider)
				_, err := reg.Resolve((*cacheCap)(nil))
				return err
			},
			says:     "no drive has started",
			supplied: "unless the host supplied it",
		},
		"in Prepare": {
			lookup: func(t *testing.T) error {
				reg := New()
				reg.Register(provider)
				var err error
				reg.Register(Module{
					Name: "early",
					Prepare: func(_ context.Context, r *Registry) (Enablement, error) {
						_, err = r.Resolve((*cacheCap)(nil))
						return Enablement{}, nil
					},
				})
				if drove := reg.Drive(t.Context(), Drive{Depth: StageNew, Participants: []string{"early", "provider"}}); drove != nil {
					t.Fatalf("the drive failed: %v", drove)
				}
				return err
			},
			says:     "Prepare",
			supplied: "only an instance the host supplied can be taken up there",
		},
		"in the action of a drive that constructs nothing": {
			lookup: func(t *testing.T) error {
				reg := New()
				reg.Register(provider)
				var err error
				if drove := reg.Drive(t.Context(), Drive{
					Depth:        StagePrepare,
					Participants: []string{"provider"},
					Action: func(_ context.Context, r *Registry) error {
						_, err = r.Resolve((*cacheCap)(nil))
						return nil
					},
				}); drove != nil {
					t.Fatalf("the drive failed: %v", drove)
				}
				return err
			},
			says:     "StagePrepare",
			supplied: "only an instance the host supplied exists in it",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.lookup(t)
			if !errors.Is(err, ErrMissingProvider) {
				t.Fatalf("Resolve returned %v, want ErrMissingProvider", err)
			}
			if !strings.Contains(err.Error(), `"provider"`) || !strings.Contains(err.Error(), c.says) {
				t.Errorf("error %q does not name the provider and say %q", err, c.says)
			}
			if !strings.Contains(err.Error(), c.supplied) {
				t.Errorf("error %q does not say %q", err, c.supplied)
			}
			if strings.Contains(err.Error(), "constructed later") {
				t.Errorf("error %q blames the construction order, and nothing is being constructed", err)
			}
		})
	}
}

// TestResolveNamesTheExclusiveFix checks that a capability claimed exclusively by modules outside
// the drive names them and the two ways to fix it.
func TestResolveNamesTheExclusiveFix(t *testing.T) {
	reg := New()
	reg.Register(building("memory", exclusive((*cacheCap)(nil))))
	reg.Register(building("redis", exclusive((*cacheCap)(nil))))
	reg.participating = map[string]bool{}

	_, err := reg.Resolve((*cacheCap)(nil))
	if !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("Resolve returned %v, want ErrMissingProvider", err)
	}
	for _, want := range []string{
		"memory", "redis", "claimed exclusively", "name one implementation",
		"hand an instance of one of them in with Supply",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestResolveFindsASuppliedInstanceFromSupplyOn checks that Resolve and ResolveAll return a supplied
// instance as soon as Supply records it.
func TestResolveFindsASuppliedInstanceFromSupplyOn(t *testing.T) {
	reg := New()
	reg.Register(Module{Name: "host-built", Provides: provides((*cacheCap)(nil))})
	want := &product{id: "from the host"}
	reg.Supply(want)

	got, err := reg.Resolve((*cacheCap)(nil))
	if err != nil {
		t.Fatalf("Resolve failed for a capability whose module has a supplied instance: %v", err)
	}
	if got != any(want) {
		t.Fatalf("Resolve returned %#v, want the supplied instance %#v", got, want)
	}
	if all := reg.ResolveAll((*cacheCap)(nil)); len(all) != 1 || all[0] != any(want) {
		t.Fatalf("ResolveAll returned %#v, want the supplied instance alone", all)
	}
}

// TestResolveAllPutsSuppliedAndBuiltInstancesInOneNameOrder checks that ResolveAll returns built
// and supplied instances together, ordered by module name.
func TestResolveAllPutsSuppliedAndBuiltInstancesInOneNameOrder(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "alpha", Provides: provides((*cacheCap)(nil))}, &product{id: "alpha"})
	reg.Register(Module{Name: "zeta", Provides: provides((*cacheCap)(nil))})
	reg.Register(Module{Name: "mid", Provides: provides((*cacheCap)(nil))})
	reg.Supply(&product{id: "zeta"}, "zeta")
	reg.Supply(&product{id: "mid"}, "mid")

	var got []string
	for _, v := range reg.ResolveAll((*cacheCap)(nil)) {
		got = append(got, v.(cacheCap).cacheID())
	}
	if want := []string{"alpha", "mid", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("ResolveAll returned %v, want %v", got, want)
	}
}

// TestResolveIsAmbiguousBetweenASuppliedAndABuiltInstance pins the single-valued rule across the
// two origins.
func TestResolveIsAmbiguousBetweenASuppliedAndABuiltInstance(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "built", Provides: provides((*cacheCap)(nil))}, &product{id: "built"})
	reg.Register(Module{Name: "supplied", Provides: provides((*cacheCap)(nil))})
	reg.Supply(&product{id: "supplied"}, "supplied")

	_, err := reg.Resolve((*cacheCap)(nil))
	if !errors.Is(err, ErrAmbiguousProvider) {
		t.Fatalf("Resolve returned %v, want ErrAmbiguousProvider", err)
	}
	for _, name := range []string{"built", "supplied"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name provider %s", err, name)
		}
	}
}

// TestResolveForAModuleWithoutNewSaysSupplyAtEveryMoment pins the cause that outranks the timing
// ones.
func TestResolveForAModuleWithoutNewSaysSupplyAtEveryMoment(t *testing.T) {
	hostOnly := Module{Name: "host-only", Provides: provides((*cacheCap)(nil))}
	cases := map[string]struct {
		lookup   func(t *testing.T) error
		declarer string
	}{
		"before the drive": {
			declarer: "host-only",
			lookup: func(t *testing.T) error {
				reg := New()
				reg.Register(hostOnly)
				_, err := reg.Resolve((*cacheCap)(nil))
				return err
			},
		},
		"in Prepare": {
			declarer: "host-only",
			lookup: func(t *testing.T) error {
				reg := New()
				reg.Register(hostOnly)
				var err error
				reg.Register(Module{
					Name: "early",
					Prepare: func(_ context.Context, r *Registry) (Enablement, error) {
						_, err = r.Resolve((*cacheCap)(nil))
						return Enablement{}, nil
					},
				})
				if drove := reg.Drive(t.Context(), Drive{Depth: StagePrepare, Participants: []string{"early", "host-only"}}); drove != nil {
					t.Fatalf("the drive failed: %v", drove)
				}
				return err
			},
		},
		"in the action of a drive that constructs nothing": {
			declarer: "host-only",
			lookup: func(t *testing.T) error {
				reg := New()
				reg.Register(hostOnly)
				var err error
				if drove := reg.Drive(t.Context(), Drive{
					Depth:        StagePrepare,
					Participants: []string{"host-only"},
					Action: func(_ context.Context, r *Registry) error {
						_, err = r.Resolve((*cacheCap)(nil))
						return nil
					},
				}); drove != nil {
					t.Fatalf("the drive failed: %v", drove)
				}
				return err
			},
		},
		"after construction began": {
			declarer: "later",
			lookup: func(t *testing.T) error {
				reg := New()
				var err error
				reg.Register(Module{
					Name: "early",
					New: func(_ context.Context, r *Registry) (any, error) {
						_, err = r.Resolve((*cacheCap)(nil))
						return nil, nil
					},
				})
				reg.Register(Module{Name: "later", Provides: provides((*cacheCap)(nil))})
				_ = reg.Drive(t.Context(), Drive{Depth: StageNew, Participants: []string{"early", "later"}})
				return err
			},
		},
		"a module the command did not include": {
			declarer: "host-only",
			lookup: func(t *testing.T) error {
				reg := New()
				reg.Register(hostOnly)
				reg.Register(Module{Name: "other"})
				var err error
				if drove := reg.Drive(t.Context(), Drive{
					Depth:        StagePrepare,
					Participants: []string{"other"},
					Action: func(_ context.Context, r *Registry) error {
						_, err = r.Resolve((*cacheCap)(nil))
						return nil
					},
				}); drove != nil {
					t.Fatalf("the drive failed: %v", drove)
				}
				return err
			},
		},
		"a module taken in for its declarations alone": {
			declarer: "host-only",
			lookup: func(t *testing.T) error {
				reg := New()
				reg.Register(hostOnly)
				reg.Register(Module{Name: "other"})
				var err error
				if drove := reg.Drive(t.Context(), Drive{
					Depth:         StagePrepare,
					Participants:  []string{"other"},
					DeclaringOnly: []Declaration{{Name: "host-only"}},
					Action: func(_ context.Context, r *Registry) error {
						_, err = r.Resolve((*cacheCap)(nil))
						return nil
					},
				}); drove != nil {
					t.Fatalf("the drive failed: %v", drove)
				}
				return err
			},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.lookup(t)
			if !errors.Is(err, ErrMissingProvider) {
				t.Fatalf("Resolve returned %v, want ErrMissingProvider", err)
			}
			for _, want := range []string{`"` + c.declarer + `"`, "no New callback", "supplied no instance", "Supply"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
			for _, wrong := range []string{
				"constructed later", "no drive has started", "StagePrepare",
				"Requires", "does not include it", "declarations alone",
			} {
				if strings.Contains(err.Error(), wrong) {
					t.Errorf("error %q carries %q, a timing fix for a module that no timing "+
						"gives an instance", err, wrong)
				}
			}
		})
	}
}

// TestResolveAllSortedByModuleName checks that ResolveAll lists instances in module-name order
// whatever order the modules were seeded in.
func TestResolveAllSortedByModuleName(t *testing.T) {
	reg := New()
	for _, name := range []string{"zeta", "alpha", "mid"} {
		seed(t, reg, Module{Name: name, Provides: provides((*cacheCap)(nil))}, &product{id: name})
	}

	var got []string
	for _, v := range reg.ResolveAll((*cacheCap)(nil)) {
		got = append(got, v.(cacheCap).cacheID())
	}
	if want := []string{"alpha", "mid", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("ResolveAll returned %v, want %v", got, want)
	}
}

// TestResolveAllEmptyReturnsEmptySlice checks that ResolveAll with no instance returns an empty
// slice, not nil.
func TestResolveAllEmptyReturnsEmptySlice(t *testing.T) {
	got := New().ResolveAll((*cacheCap)(nil))
	if got == nil {
		t.Fatal("ResolveAll returned nil, want an empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("ResolveAll returned %v, want empty", got)
	}
}

// TestResourcesMatchByAssignabilityInterface checks that a resource target interface collects every
// declaration implementing it.
func TestResourcesMatchByAssignabilityInterface(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "a", Resources: []any{schemaRes{Namespace: "cache"}}}, nil)
	seed(t, reg, Module{Name: "b", Resources: []any{specRes{Path: "/v1"}}}, nil)
	seed(t, reg, Module{Name: "c", Resources: []any{otherRes{Label: "x"}}}, nil)

	got := reg.Resources((*namedRes)(nil))
	if len(got) != 3 {
		t.Fatalf("Resources collected %d declarations, want 3", len(got))
	}
}

// TestResourcesMatchByAssignabilityConcrete checks that a concrete resource target collects only
// declarations of that type.
func TestResourcesMatchByAssignabilityConcrete(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "a", Resources: []any{schemaRes{Namespace: "cache"}}}, nil)
	seed(t, reg, Module{Name: "b", Resources: []any{specRes{Path: "/v1"}}}, nil)

	got := reg.Resources((*schemaRes)(nil))
	if len(got) != 1 {
		t.Fatalf("Resources collected %d declarations, want only the schema one", len(got))
	}
	if got[0].Value.(schemaRes).Namespace != "cache" {
		t.Fatalf("Resources returned %v, want the cache schema", got[0].Value)
	}
}

// TestResourcesCarryModuleName checks that each collected resource is tagged with its declaring
// module's name.
func TestResourcesCarryModuleName(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "declarer", Resources: []any{schemaRes{Namespace: "cache"}}}, nil)

	got := reg.Resources((*schemaRes)(nil))
	if len(got) != 1 || got[0].Module != "declarer" {
		t.Fatalf("Resources returned %v, want it tagged with declarer", got)
	}
}

// TestResourcesSortedByModuleName checks that Resources sorts declarations by module name.
func TestResourcesSortedByModuleName(t *testing.T) {
	reg := New()
	for _, name := range []string{"zeta", "alpha", "mid"} {
		seed(t, reg, Module{Name: name, Resources: []any{schemaRes{Namespace: name}}}, nil)
	}

	var got []string
	for _, res := range reg.Resources((*schemaRes)(nil)) {
		got = append(got, res.Module)
	}
	if want := []string{"alpha", "mid", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("Resources returned %v, want %v", got, want)
	}
}

// TestResourcesKeepsMultipleFromOneModule checks that Resources keeps every declaration of one
// module in declaration order.
func TestResourcesKeepsMultipleFromOneModule(t *testing.T) {
	reg := New()
	seed(t, reg, Module{
		Name:      "twice",
		Resources: []any{schemaRes{Namespace: "first"}, schemaRes{Namespace: "second"}},
	}, nil)

	got := reg.Resources((*schemaRes)(nil))
	if len(got) != 2 {
		t.Fatalf("Resources returned %d declarations, want both", len(got))
	}
	if got[0].Value.(schemaRes).Namespace != "first" || got[1].Value.(schemaRes).Namespace != "second" {
		t.Fatalf("Resources reordered a module's own declarations: %v", got)
	}
}

// TestResourcesConcreteTargetRequiresTypeIdentity pins the concrete half of the match rule.
func TestResourcesConcreteTargetRequiresTypeIdentity(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "underlying", Resources: []any{[]string{"a", "b"}}}, nil)

	if got := reg.Resources((*resNames)(nil)); len(got) != 0 {
		t.Fatalf("Resources collected %v for a target the values are not, want none", got)
	}
	if got := Resources[resNames](reg); len(got) != 0 {
		t.Fatalf("Resources[resNames] collected %v, want none", got)
	}
}

// TestResourcesConcreteTargetRejectsChannelDirection checks that a channel direction a value does
// not have collects nothing, through both the method and the generic function.
func TestResourcesConcreteTargetRejectsChannelDirection(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "signals", Resources: []any{make(chan int)}}, nil)

	if got := reg.Resources((*<-chan int)(nil)); len(got) != 0 {
		t.Fatalf("Resources collected %v for a direction the values do not have, want none", got)
	}
	if got := Resources[<-chan int](reg); len(got) != 0 {
		t.Fatalf("Resources[<-chan int] collected %v, want none", got)
	}
}

// TestResourcesInterfaceTargetKeepsAssignability checks that an interface resource target collects
// both implementations and skips an unrelated value.
func TestResourcesInterfaceTargetKeepsAssignability(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "a", Resources: []any{schemaRes{Namespace: "cache"}}}, nil)
	seed(t, reg, Module{Name: "b", Resources: []any{specRes{Path: "/v1"}}}, nil)
	seed(t, reg, Module{Name: "c", Resources: []any{[]string{"not a namedRes"}}}, nil)

	got := Resources[namedRes](reg)
	if len(got) != 2 {
		t.Fatalf("Resources[namedRes] collected %v, want both implementations", got)
	}
	if got[0].Value.resourceName() != "schema:cache" || got[1].Value.resourceName() != "spec:/v1" {
		t.Fatalf("Resources[namedRes] returned %v, want the schema and the spec", got)
	}
}

// TestGenericResolveAllPanicsOnNonInterface checks that ResolveAll with a concrete type argument
// panics, explaining that the token must point to an interface.
func TestGenericResolveAllPanicsOnNonInterface(t *testing.T) {
	reg := New()
	assertPanics(t, "must point to an interface", func() {
		ResolveAll[int](reg)
	})
}

// TestGenericResourcesAllowsConcreteType checks that Resources accepts a concrete type argument and
// returns its declarations.
func TestGenericResourcesAllowsConcreteType(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "a", Resources: []any{schemaRes{Namespace: "cache"}}}, nil)

	got := Resources[schemaRes](reg)
	if len(got) != 1 || got[0].Value.Namespace != "cache" {
		t.Fatalf("Resources[schemaRes] returned %v, want the cache schema", got)
	}
}

// TestGenericResolveReturnsTypedInstance checks that Resolve returns the instance typed as the
// capability interface.
func TestGenericResolveReturnsTypedInstance(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "a", Provides: provides((*cacheCap)(nil))}, &product{id: "a"})

	got, err := Resolve[cacheCap](reg)
	if err != nil {
		t.Fatalf("Resolve[cacheCap] failed: %v", err)
	}
	if got.cacheID() != "a" {
		t.Fatalf("Resolve[cacheCap] returned %v, want a's product", got)
	}
}

// TestEnablementMissingName checks that Enablement reports false for a name with no verdict.
func TestEnablementMissingName(t *testing.T) {
	reg := New()
	reg.enablement = map[string]Enablement{"known": {State: StateEnabled}}

	if _, ok := reg.Enablement("unknown"); ok {
		t.Fatal("Enablement reported a name that was never registered")
	}
}

// TestEnablementReportsDisabledWithReason checks that Enablement returns a disabled verdict with
// its reason.
func TestEnablementReportsDisabledWithReason(t *testing.T) {
	reg := New()
	reg.enablement = map[string]Enablement{
		"off": {State: StateDisabled, Reason: "no connection address configured"},
	}

	got, ok := reg.Enablement("off")
	if !ok {
		t.Fatal("Enablement missed a module it holds a verdict for")
	}
	if got.State != StateDisabled || got.Reason != "no connection address configured" {
		t.Fatalf("Enablement returned %+v, want the disabled verdict with its reason", got)
	}
}

// TestEnablementBeforeResolutionReportsNotReady pins that the query promises the final verdict,
// and before resolution there is none to give.
func TestEnablementBeforeResolutionReportsNotReady(t *testing.T) {
	reg := New()
	reg.Register(Module{Name: "pending"})

	if _, ok := reg.Enablement("pending"); ok {
		t.Fatal("Enablement answered before resolution had run")
	}
}

// TestResolveMatchesCapabilityByTypeIdentity pins how one capability token matches another: by the
// identity of the element type, not by assignability.
func TestResolveMatchesCapabilityByTypeIdentity(t *testing.T) {
	reg := New()
	seed(t, reg, Module{Name: "super", Provides: provides((*superCacheCap)(nil))}, &product{id: "super"})

	if _, err := reg.Resolve((*cacheCap)(nil)); !errors.Is(err, ErrMissingProvider) {
		t.Fatalf("resolving the embedded capability returned %v, want ErrMissingProvider", err)
	}
	got, err := reg.Resolve((*superCacheCap)(nil))
	if err != nil {
		t.Fatalf("resolving the declared capability failed: %v", err)
	}
	if got.(*product).id != "super" {
		t.Fatalf("the declared capability resolved to %#v, want the module's own product", got)
	}
}

// TestADriveThatNamesOneModuleTwiceKeepsBothAdjustments pins where the declaration list is folded.
func TestADriveThatNamesOneModuleTwiceKeepsBothAdjustments(t *testing.T) {
	reg := New()
	reg.Register(Module{Name: "collected"})

	if err := reg.Drive(t.Context(), Drive{DeclaringOnly: []Declaration{
		{Name: "collected"},
		{Name: "collected", AllResources: true},
	}}); err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if !reg.AllResourcesInForce("collected") {
		t.Error("a Drive that named one module twice, once with the override, lost the " +
			"override")
	}
}

// TestAModuleADriveNamesAtBothLevelsIsAParticipant pins which level wins when one Drive names a
// module as both.
func TestAModuleADriveNamesAtBothLevelsIsAParticipant(t *testing.T) {
	reg := New()
	reg.Register(Module{Name: "both"})

	if err := reg.Drive(t.Context(), Drive{
		Participants:  []string{"both"},
		DeclaringOnly: []Declaration{{Name: "both", AllResources: true}},
	}); err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if !reg.Participating("both") {
		t.Error("a module this drive runs was not counted as taking part")
	}
	if reg.AllResourcesInForce("both") {
		t.Error("a module this drive runs was also recorded as collected, so the two readings " +
			"are both available for one module")
	}
}
