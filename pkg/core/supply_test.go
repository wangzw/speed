package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/internal/regprobe"
	dotted "github.com/vislake/speed/pkg/core/internal/regprobe/dotted.v1"
)

const (
	testSite     = "github.com/vislake/speed/pkg/core_test"
	regprobeSite = "github.com/vislake/speed/pkg/core/internal/regprobe"
	dottedSite   = regprobeSite + "/dotted.v1"
)

func placeholder(name string, provides ...core.Token) core.Module {
	m := core.Module{Name: name}
	for _, token := range provides {
		m.Provides = append(m.Provides, core.Provision{Token: token})
	}
	return m
}

func capturing(m core.Module, dst *any) core.Module {
	m.Init = func(_ context.Context, _ *core.Registry, instance any) error {
		*dst = instance
		return nil
	}
	return m
}

func takenIn(t *testing.T, reg *core.Registry) []string {
	t.Helper()
	if err := driveToEnd(t, reg, core.Drive{}); err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	var names []string
	for _, m := range reg.Modules() {
		if reg.Participating(m.Name) {
			names = append(names, m.Name)
		}
	}
	slices.Sort(names)
	return names
}

func assertTakenIn(t *testing.T, reg *core.Registry, want ...string) {
	t.Helper()
	if got := takenIn(t, reg); !slices.Equal(got, want) {
		t.Fatalf("the modules that took part without being named are %v, want %v", got, want)
	}
}

func assertMentions(t *testing.T, msg string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(msg, want) {
			t.Errorf("panic %q does not mention %q", msg, want)
		}
	}
}

func assertOmits(t *testing.T, msg string, unwanted ...string) {
	t.Helper()
	for _, fragment := range unwanted {
		if strings.Contains(msg, fragment) {
			t.Errorf("panic %q mentions %q", msg, fragment)
		}
	}
}

func assertCalledFrom(t *testing.T, msg, site string) {
	t.Helper()
	assertMentions(t, msg, "Supply from "+site+" ")
}

type wrapped struct{ *regprobe.Product }

type registrationRoute struct {
	name     string
	site     string
	register func(*core.Registry, core.Module)
	instance any
}

func registrationRoutes() []registrationRoute {
	return []registrationRoute{
		{"a function", regprobeSite, regprobe.Register, &regprobe.Product{ID: "probe"}},
		{"a method", regprobeSite, regprobe.Registrar{}.Register, &regprobe.Product{ID: "probe"}},
		{"a function literal", regprobeSite, regprobe.RegisterInClosure, &regprobe.Product{ID: "probe"}},
		{"a function in a package whose last path element contains a dot", dottedSite, dotted.Register, &dotted.Product{ID: "dotted"}},
		{"a method in a package whose last path element contains a dot", dottedSite, dotted.Registrar{}.Register, &dotted.Product{ID: "dotted"}},
		{"a function literal in a package whose last path element contains a dot", dottedSite, dotted.RegisterInClosure, &dotted.Product{ID: "dotted"}},
	}
}

// TestSupplyClaimsTheOnlyModuleTheInstanceSatisfies checks that an unnamed supply picks the only
// registered module whose capabilities the instance implements.
func TestSupplyClaimsTheOnlyModuleTheInstanceSatisfies(t *testing.T) {
	reg := core.New()
	register(reg,
		placeholder("cache-mod", (*Cache)(nil)),
		placeholder("reading-mod", (*Reader)(nil)),
		core.Module{Name: "bare"},
	)

	reg.Supply(&box{id: "claimed"})

	assertTakenIn(t, reg, "cache-mod")
}

// TestSupplyClaimsASingleCandidateWhicheverPackageRegisteredIt pins that the package that defines
// the instance's type is consulted only to tell several candidates apart.
func TestSupplyClaimsASingleCandidateWhicheverPackageRegisteredIt(t *testing.T) {
	for _, route := range registrationRoutes() {
		t.Run(route.name, func(t *testing.T) {
			reg := core.New()
			route.register(reg, placeholder("cache-mod", (*Cache)(nil)))

			reg.Supply(&box{id: "claimed"})

			assertTakenIn(t, reg, "cache-mod")
		})
	}
}

// TestSupplyClaimsOnlyAModuleWhoseEveryCapabilityTheInstanceSatisfies pins that being satisfied in
// part makes no candidate.
func TestSupplyClaimsOnlyAModuleWhoseEveryCapabilityTheInstanceSatisfies(t *testing.T) {
	reg := core.New()
	register(reg,
		placeholder("wide", (*Cache)(nil), (*Reader)(nil)),
		placeholder("narrow", (*Cache)(nil)),
	)

	reg.Supply(&box{id: "claimed"})

	assertTakenIn(t, reg, "narrow")
}

// TestSupplyClaimsAModuleWhoseSeveralCapabilitiesTheInstanceMeetsInFull checks that an unnamed
// supply picks a module whose every capability the instance implements.
func TestSupplyClaimsAModuleWhoseSeveralCapabilitiesTheInstanceMeetsInFull(t *testing.T) {
	reg := core.New()
	register(reg,
		placeholder("pair", (*Cache)(nil), (*Store)(nil)),
		placeholder("reading-mod", (*Reader)(nil)),
	)

	reg.Supply(&box{id: "claimed"})

	assertTakenIn(t, reg, "pair")
}

// TestSupplyBreaksATieByThePackageThatRegisteredTheModule pins the second step of a claim by
// instance.
func TestSupplyBreaksATieByThePackageThatRegisteredTheModule(t *testing.T) {
	for _, route := range registrationRoutes() {
		t.Run(route.name, func(t *testing.T) {
			reg := core.New()
			register(reg, placeholder("zulu", (*Cache)(nil)))
			route.register(reg, placeholder("mid", (*Cache)(nil)))
			register(reg, placeholder("alpha", (*Cache)(nil)))

			reg.Supply(route.instance)

			assertTakenIn(t, reg, "mid")
		})
	}
}

// TestSupplyClaimsATypedNilPointerByItsDynamicType pins that a typed nil is an instance like any
// other for the claim: it carries a type, and the type is what the claim looks at.
func TestSupplyClaimsATypedNilPointerByItsDynamicType(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("zulu", (*Cache)(nil)))
	regprobe.Register(reg, placeholder("mid", (*Cache)(nil)))
	register(reg, placeholder("alpha", (*Cache)(nil)))

	reg.Supply((*regprobe.Product)(nil))

	assertTakenIn(t, reg, "mid")
}

// TestSupplyGivesAnInstanceOfAHostDefinedTypeToTheModuleTheHostRegistered checks that the
// registration site's package decides between two modules declaring the same capability.
func TestSupplyGivesAnInstanceOfAHostDefinedTypeToTheModuleTheHostRegistered(t *testing.T) {
	reg := core.New()
	regprobe.Register(reg, placeholder("in-probe", (*Cache)(nil)))
	register(reg, placeholder("in-test", (*Cache)(nil)))

	reg.Supply(&box{id: "host"})

	assertTakenIn(t, reg, "in-test")
}

// TestSupplyPanicsWhenTheTypesPackageDoesNotSettleTheClaim checks that an ambiguous unnamed supply
// panics naming the candidates and their sites, and that a name settles it.
func TestSupplyPanicsWhenTheTypesPackageDoesNotSettleTheClaim(t *testing.T) {
	cache := (*Cache)(nil)
	cases := []struct {
		name     string
		setup    func(reg *core.Registry)
		instance any
		mentions []string
	}{
		{
			name: "none of the candidates is registered from the package that defines the type",
			setup: func(reg *core.Registry) {
				regprobe.Register(reg, placeholder("cand-a", cache))
				dotted.Register(reg, placeholder("cand-b", cache))
			},
			instance: &box{id: "host"},
			mentions: []string{testSite, "cand-a", regprobeSite, "cand-b", dottedSite},
		},
		{
			name: "several candidates are registered from the package that defines the type",
			setup: func(reg *core.Registry) {
				regprobe.Register(reg, placeholder("cand-a", cache))
				regprobe.Register(reg, placeholder("cand-b", cache))
			},
			instance: &regprobe.Product{ID: "probe"},
			mentions: []string{testSite, "cand-a", "cand-b", regprobeSite},
		},
		{
			name: "the type and every candidate are in the package of the test",
			setup: func(reg *core.Registry) {
				register(reg, placeholder("cand-a", cache), placeholder("cand-b", cache))
			},
			instance: &box{id: "host"},
			mentions: []string{testSite, "cand-a", "cand-b"},
		},
		{
			name: "the host wraps the type, so the type is defined in the host's package",
			setup: func(reg *core.Registry) {
				regprobe.Register(reg, placeholder("cand-a", cache))
				regprobe.Register(reg, placeholder("cand-b", cache))
			},
			instance: wrapped{Product: &regprobe.Product{ID: "probe"}},
			mentions: []string{testSite, "cand-a", "cand-b", regprobeSite},
		},
		{
			name: "the type is written out in place, so no package defines it",
			setup: func(reg *core.Registry) {
				regprobe.Register(reg, placeholder("cand-a", cache))
				regprobe.Register(reg, placeholder("cand-b", cache))
			},
			instance: struct{ *regprobe.Product }{&regprobe.Product{ID: "probe"}},
			mentions: []string{testSite, "cand-a", "cand-b", regprobeSite},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := core.New()
			c.setup(reg)

			msg := recoverMessage(t, func() { reg.Supply(c.instance) })

			assertCalledFrom(t, msg, testSite)
			assertMentions(t, msg, c.mentions...)
			assertMentions(t, msg, "Name the module")
			reg.Supply(c.instance, "cand-b")
			assertTakenIn(t, reg, "cand-b")
		})
	}
}

// TestSupplyReportsTheNearCandidatesWhenThereIsNoCandidate pins what a miss says.
func TestSupplyReportsTheNearCandidatesWhenThereIsNoCandidate(t *testing.T) {
	reg := core.New()
	register(reg,
		placeholder("near-a", (*Cache)(nil), (*Reader)(nil)),
		placeholder("near-b", (*Store)(nil), (*Intake)(nil)),
		placeholder("unrelated", (*Reader)(nil)),
		core.Module{Name: "bare"},
	)

	msg := recoverMessage(t, func() { regprobe.Supply(reg, &box{id: "host"}) })

	assertCalledFrom(t, msg, regprobeSite)
	assertMentions(t, msg, "near-a", "Reader", "near-b", "Intake")
	assertOmits(t, msg, "unrelated", "bare", "Cache", "Store")
	at := func(fragment string) int { return strings.Index(msg, fragment) }
	if at("near-a") >= at("Reader") || at("Reader") >= at("near-b") || at("near-b") >= at("Intake") {
		t.Errorf("panic %q does not pair each near candidate with the capability it lacks", msg)
	}
	assertNoCandidateAdvice(t, msg)
	assertMentions(t, msg, "Naming one of them does not help")

	msg = recoverMessage(t, func() { reg.Supply(&box{id: "host"}, "near-a") })
	assertMentions(t, msg, "near-a", "Reader")
	reg.Supply(&box{id: "host"}, "bare")
	assertTakenIn(t, reg, "bare")
}

// TestSupplyWithoutAnyNearCandidateStillPanics pins that a refusal with nothing near to report
// still says why and what to do.
func TestSupplyWithoutAnyNearCandidateStillPanics(t *testing.T) {
	cases := map[string]func(reg *core.Registry){
		"a registry with no module": func(*core.Registry) {},
		"a registry whose modules declare something else": func(reg *core.Registry) {
			register(reg, placeholder("reading-mod", (*Reader)(nil)))
		},
		"a registry whose only module declares nothing": func(reg *core.Registry) {
			register(reg, core.Module{Name: "resources-only"})
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			reg := core.New()
			setup(reg)

			msg := recoverMessage(t, func() { regprobe.Supply(reg, &box{id: "host"}) })

			assertCalledFrom(t, msg, regprobeSite)
			assertMentions(t, msg, "registered before an instance is supplied")
			assertNoCandidateAdvice(t, msg)
			assertOmits(t, msg, "Naming one of them")
			assertTakenIn(t, reg)
		})
	}
}

func assertNoCandidateAdvice(t *testing.T, msg string) {
	t.Helper()
	assertMentions(t, msg, "every capability the module declares", "declares nothing")
	assertOmits(t, msg, "in part")
}

// TestSupplyPanicsReadTheSameWhateverTheOrderOfRegistration pins that what a refusal says does not
// depend on the order the modules were registered in.
func TestSupplyPanicsReadTheSameWhateverTheOrderOfRegistration(t *testing.T) {
	cache := (*Cache)(nil)
	type entry struct {
		module   core.Module
		register func(*core.Registry, core.Module)
	}
	scenarios := []struct {
		name    string
		entries []entry
	}{
		{"candidates the package of the type does not settle", []entry{
			{placeholder("cand-a", cache), regprobe.Register},
			{placeholder("cand-b", cache), dotted.Register},
			{placeholder("cand-c", cache), regprobe.Register},
		}},
		{"near candidates", []entry{
			{placeholder("near-a", cache, (*Reader)(nil)), regprobe.Register},
			{placeholder("near-b", (*Store)(nil), (*Intake)(nil)), regprobe.Register},
			{placeholder("near-c", (*Greeter)(nil), (*Reader)(nil)), regprobe.Register},
		}},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			var first string
			for _, order := range orders(len(s.entries)) {
				reg := core.New()
				for _, i := range order {
					s.entries[i].register(reg, s.entries[i].module)
				}

				msg := recoverMessage(t, func() { reg.Supply(&box{id: "host"}) })

				if first == "" {
					first = msg
				} else if msg != first {
					t.Fatalf("registered in the order %v the panic reads\n%s\nwhere another order gave\n%s",
						order, msg, first)
				}
			}
		})
	}
}

func orders(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, rest := range orders(n - 1) {
		for at := 0; at <= len(rest); at++ {
			out = append(out, slices.Insert(slices.Clone(rest), at, n-1))
		}
	}
	return out
}

// TestSupplyByNameOverridesTheClaimByType checks that a name picks the module even when the type's
// package would have chosen another.
func TestSupplyByNameOverridesTheClaimByType(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("zulu", (*Cache)(nil)))
	regprobe.Register(reg, placeholder("mid", (*Cache)(nil)))
	register(reg, placeholder("alpha", (*Cache)(nil)))

	reg.Supply(&regprobe.Product{ID: "probe"}, "alpha")

	assertTakenIn(t, reg, "alpha")
}

// TestSupplyByNameAcceptsAModuleThatDeclaresNothing pins that only a name reaches a module without
// Provides, and that such a module takes part and receives the instance like any other.
func TestSupplyByNameAcceptsAModuleThatDeclaresNothing(t *testing.T) {
	p := &probe{}
	reg := core.New()
	var received any
	m := p.module("resources-only")
	m.New = nil
	register(reg, capturing(m, &received))
	handed := &box{id: "handed in"}

	reg.Supply(handed, "resources-only")

	if err := driveToEnd(t, reg, core.Drive{Depth: core.StageInit}); err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if received != handed {
		t.Fatalf("the module received %#v, want the instance that was supplied", received)
	}
	if got := p.snapshot(); !slices.Contains(got, "migrate resources-only") {
		t.Fatalf("the module did not take part; recorded %v", got)
	}
}

// TestSupplyTakesTheEmptyStringAsAName checks that an empty name panics at the call site and
// supplies nothing.
func TestSupplyTakesTheEmptyStringAsAName(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("cache-mod", (*Cache)(nil)))

	msg := recoverMessage(t, func() { reg.Supply(&box{id: "host"}, "") })

	assertMentions(t, msg, testSite)
	assertTakenIn(t, reg)
}

// TestSupplyByNameRejectsAnUnregisteredName checks that an unregistered name panics naming it and
// supplies nothing.
func TestSupplyByNameRejectsAnUnregisteredName(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("cache-mod", (*Cache)(nil)))

	msg := recoverMessage(t, func() { reg.Supply(&box{id: "host"}, "ghost") })

	assertMentions(t, msg, testSite, "ghost")
	assertTakenIn(t, reg)
}

// TestSupplyTakesAtMostOneName pins that the number of names is checked on its own account.
func TestSupplyTakesAtMostOneName(t *testing.T) {
	for _, c := range []struct {
		name  string
		names []string
	}{
		{"two different names", []string{"alpha", "bravo"}},
		{"one name twice", []string{"alpha", "alpha"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := core.New()
			register(reg, placeholder("alpha", (*Cache)(nil)), core.Module{Name: "bravo"})

			msg := recoverMessage(t, func() { reg.Supply(&box{id: "host"}, c.names...) })

			assertMentions(t, msg, testSite, "names 2 modules")
			if _, err := core.Resolve[Cache](reg); !errors.Is(err, core.ErrMissingProvider) {
				t.Errorf("Resolve[Cache] returned %v after the refusal, want ErrMissingProvider", err)
			}
			assertTakenIn(t, reg)
		})
	}
}

// TestSupplyByNameRejectsAnInstanceThatMissesACapability checks that a named supply panics naming
// only the capabilities the instance lacks.
func TestSupplyByNameRejectsAnInstanceThatMissesACapability(t *testing.T) {
	for _, c := range []struct {
		name     string
		declares []core.Token
		lacks    []string
		delivers []string
	}{
		{"one of two", []core.Token{(*Cache)(nil), (*Reader)(nil)}, []string{"Reader"}, []string{"Cache"}},
		{
			"two of four",
			[]core.Token{(*Cache)(nil), (*Reader)(nil), (*Intake)(nil), (*Store)(nil)},
			[]string{"Reader", "Intake"},
			[]string{"Cache", "Store"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := core.New()
			register(reg, placeholder("wide", c.declares...))

			msg := recoverMessage(t, func() { regprobe.Supply(reg, &box{id: "host"}, "wide") })

			assertCalledFrom(t, msg, regprobeSite)
			assertMentions(t, msg, append([]string{"wide"}, c.lacks...)...)
			assertOmits(t, msg, c.delivers...)
			assertTakenIn(t, reg)
		})
	}
}

// TestSupplyRejectsANilInstance checks that supplying nil panics with or without a name and
// supplies nothing.
func TestSupplyRejectsANilInstance(t *testing.T) {
	for _, c := range []struct {
		name  string
		names []string
	}{
		{"without a name", nil},
		{"with a name", []string{"cache-mod"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := core.New()
			register(reg, placeholder("cache-mod", (*Cache)(nil)))

			msg := recoverMessage(t, func() { reg.Supply(nil, c.names...) })

			assertMentions(t, msg, testSite, "nil instance")
			assertTakenIn(t, reg)
		})
	}
}

// TestSupplyTakesATypedNilPointerAsAnInstance pins that only an interface value that is itself nil
// counts as no instance.
func TestSupplyTakesATypedNilPointerAsAnInstance(t *testing.T) {
	p := &probe{}
	reg := core.New()
	m := placeholder("cache-mod", (*Cache)(nil))
	m.Init = func(_ context.Context, _ *core.Registry, instance any) error {
		typed, isBox := instance.(*box)
		p.record("init typed=%v nil=%v", isBox, typed == nil)
		return nil
	}
	register(reg, m)

	reg.Supply((*box)(nil))

	if err := driveToEnd(t, reg, core.Drive{Depth: core.StageInit}); err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	if want := []string{"init typed=true nil=true"}; !slices.Equal(p.snapshot(), want) {
		t.Fatalf("the callback saw %v, want %v", p.snapshot(), want)
	}
}

// TestSupplyDecidesWhetherTwoInstancesAreOneObjectByReferenceIdentity pins which pairs the
// same-object check refuses and which it lets through.
func TestSupplyDecidesWhetherTwoInstancesAreOneObjectByReferenceIdentity(t *testing.T) {
	outer := &outerBox{id: "outer"}
	if reflect.ValueOf(outer).Pointer() != reflect.ValueOf(&outer.inner).Pointer() {
		t.Fatal("the first field of a struct does not share the struct's address, " +
			"so the struct-and-first-field case below pins nothing")
	}
	shared := &box{id: "shared"}
	sharedMap := mapBox{"k": "v"}
	sharedChan := make(chanBox)
	void := &voidBox{}
	held := heldBox{items: []string{"a"}}
	slice := sliceBox{"a"}
	fn := funcBox(func() {})

	for _, c := range []struct {
		name    string
		first   any
		second  any
		refused bool
	}{
		{"the same pointer", shared, shared, true},
		{"the same map", sharedMap, sharedMap, true},
		{"the same channel", sharedChan, sharedChan, true},
		{"two pointers to equal structs", &box{id: "a"}, &box{id: "a"}, false},
		{"two equal maps", mapBox{"k": "v"}, mapBox{"k": "v"}, false},
		{"two channels", make(chanBox), make(chanBox), false},
		{"a struct and a pointer to its first field", outer, &outer.inner, false},
		{"the same pointer to a zero-size struct", void, void, false},
		{"the same value of a zero-size type", voidBox{}, voidBox{}, false},
		{"the same value of a struct type", plainBox{id: "a"}, plainBox{id: "a"}, false},
		{"the same value of a struct type that cannot be compared", held, held, false},
		{"the same slice", slice, slice, false},
		{"the same function", fn, fn, false},
		{"a nil pointer twice", (*box)(nil), (*box)(nil), false},
		{"a nil map twice", mapBox(nil), mapBox(nil), false},
		{"a nil channel twice", chanBox(nil), chanBox(nil), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := core.New()
			register(reg, placeholder("first", (*Cache)(nil)), placeholder("second", (*Store)(nil)))
			regprobe.Supply(reg, c.first, "first")

			if !c.refused {
				dotted.Supply(reg, c.second, "second")
				assertTakenIn(t, reg, "first", "second")
				return
			}
			msg := recoverMessage(t, func() { dotted.Supply(reg, c.second, "second") })
			assertCalledFrom(t, msg, dottedSite)
			assertMentions(t, msg, fmt.Sprintf("module %q, from %s.", "first", regprobeSite))
			if _, err := core.Resolve[Store](reg); !errors.Is(err, core.ErrMissingProvider) {
				t.Errorf("Resolve[Store] returned %v after the refusal, want ErrMissingProvider", err)
			}
			assertTakenIn(t, reg, "first")
		})
	}
}

// TestTheSameObjectMayBeSuppliedToTwoRegistries checks that one object may be supplied to several
// registries without a duplicate-object panic.
func TestTheSameObjectMayBeSuppliedToTwoRegistries(t *testing.T) {
	shared := &box{id: "shared"}
	for _, module := range []string{"cache-mod", "cache-mod", "other-mod"} {
		reg := core.New()
		register(reg, placeholder(module, (*Cache)(nil)))

		reg.Supply(shared)

		assertTakenIn(t, reg, module)
	}
}

// TestTwoModulesMayConstructTheSameObject pins that the check covers Supply alone.
func TestTwoModulesMayConstructTheSameObject(t *testing.T) {
	shared := &box{id: "shared"}
	construct := func(context.Context, *core.Registry) (any, error) { return shared, nil }
	reg := core.New()
	register(reg,
		core.Module{Name: "one", Provides: []core.Provision{{Token: (*Cache)(nil)}}, New: construct},
		core.Module{Name: "two", Provides: []core.Provision{{Token: (*Store)(nil)}}, New: construct},
	)

	err := driveToEnd(t, reg, core.Drive{Depth: core.StageNew, Participants: []string{"one", "two"}})
	if err != nil {
		t.Fatalf("Drive returned %v, want the registry not to look at what New returns", err)
	}
}

// TestSupplyTwiceToOneModulePanicsNamingBothCalls pins that a module has at most one supplied
// instance, however the call found the module, and that the panic names the package of each call.
func TestSupplyTwiceToOneModulePanicsNamingBothCalls(t *testing.T) {
	routes := []struct {
		name   string
		site   string
		supply func(*core.Registry, any, ...string)
	}{
		{"a package", regprobeSite, regprobe.Supply},
		{"a package whose last path element contains a dot", dottedSite, dotted.Supply},
	}
	claims := []struct {
		name  string
		names []string
	}{
		{"by name", []string{"cache-mod"}},
		{"by instance", nil},
	}
	for _, route := range routes {
		for _, claim := range claims {
			t.Run(route.name+" "+claim.name, func(t *testing.T) {
				var received any
				reg := core.New()
				register(reg, capturing(placeholder("cache-mod", (*Cache)(nil)), &received),
					core.Module{Name: "other-mod"})
				first := &box{id: "first"}
				route.supply(reg, first, claim.names...)
				second := &box{id: "second"}

				msg := recoverMessage(t, func() { reg.Supply(second, claim.names...) })

				assertMentions(t, msg, route.site, testSite, "cache-mod")
				reg.Supply(second, "other-mod")
				if err := driveToEnd(t, reg, core.Drive{Depth: core.StageInit}); err != nil {
					t.Fatalf("Drive returned %v", err)
				}
				if received != first {
					t.Fatalf("the module received %#v, want the first instance that was supplied", received)
				}
			})
		}
	}
}

// TestSupplyBeforeTheModuleIsRegisteredFollowsTheOrderOfCalls pins the one order the mechanism
// supports.
func TestSupplyBeforeTheModuleIsRegisteredFollowsTheOrderOfCalls(t *testing.T) {
	t.Run("a name registered later is refused", func(t *testing.T) {
		reg := core.New()

		msg := recoverMessage(t, func() { reg.Supply(&box{id: "host"}, "late") })

		assertMentions(t, msg, testSite, "late")
		reg.Register(placeholder("late", (*Cache)(nil)))
		assertTakenIn(t, reg)
	})
	t.Run("a claim before any registration finds no candidate", func(t *testing.T) {
		reg := core.New()

		msg := recoverMessage(t, func() { regprobe.Supply(reg, &box{id: "host"}) })

		assertCalledFrom(t, msg, regprobeSite)
	})
	t.Run("a module registered after the claim does not take the instance over", func(t *testing.T) {
		reg := core.New()
		regprobe.Register(reg, placeholder("early", (*Cache)(nil)))
		reg.Supply(&box{id: "host"})

		register(reg, placeholder("late", (*Cache)(nil)))

		assertTakenIn(t, reg, "early")
	})
}

// TestSupplyAfterTheDriveStartedPanics pins the boundary the mechanism rests on: the host hands
// its instances in before the drive.
func TestSupplyAfterTheDriveStartedPanics(t *testing.T) {
	late := &box{id: "late"}
	supplyLate := func(r *core.Registry) { r.Supply(late, "target") }
	newRegistry := func() *core.Registry {
		reg := core.New()
		register(reg, placeholder("target", (*Cache)(nil)))
		return reg
	}
	control := newRegistry()
	control.Supply(late, "target")

	for _, c := range []struct {
		name  string
		drive func(reg *core.Registry) error
	}{
		{"from Prepare", func(reg *core.Registry) error {
			register(reg, core.Module{
				Name: "caller",
				Prepare: func(_ context.Context, r *core.Registry) (core.Enablement, error) {
					supplyLate(r)
					return core.Enablement{}, nil
				},
			})
			return reg.Drive(t.Context(), core.Drive{Depth: core.StagePrepare, Participants: []string{"caller"}})
		}},
		{"from New", func(reg *core.Registry) error {
			register(reg, core.Module{
				Name: "caller",
				New: func(_ context.Context, r *core.Registry) (any, error) {
					supplyLate(r)
					return nil, nil
				},
			})
			return reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: []string{"caller"}})
		}},
		{"from the action", func(reg *core.Registry) error {
			return reg.Drive(t.Context(), core.Drive{
				Action: func(_ context.Context, r *core.Registry) error {
					supplyLate(r)
					return nil
				},
			})
		}},
		{"after the drive returned", func(reg *core.Registry) error {
			if err := reg.Drive(t.Context(), core.Drive{}); err != nil {
				return err
			}
			supplyLate(reg)
			return nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := newRegistry()

			msg := recoverMessage(t, func() { _ = c.drive(reg) })

			assertMentions(t, msg, testSite)
			if !strings.Contains(strings.ToLower(msg), "drive") {
				t.Errorf("panic %q does not say the call came after the drive started", msg)
			}
		})
	}
}

// TestASuppliedInstanceIsTakenUpBeforeTheDriveBegins pins that the instance exists from the call,
// not from construction: both lookups answer with it before any drive.
func TestASuppliedInstanceIsTakenUpBeforeTheDriveBegins(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("reading-mod", (*Reader)(nil)))
	reg.Supply(reader{})

	got, err := core.Resolve[Reader](reg)
	if err != nil {
		t.Fatalf("taking up a supplied instance returned %v", err)
	}
	if got.Value("key") != "value:key" {
		t.Fatalf("Resolve returned %#v, want the supplied reader", got)
	}
	if all := core.ResolveAll[Reader](reg); len(all) != 1 {
		t.Fatalf("ResolveAll returned %d instances, want the one that was supplied", len(all))
	}
}

// TestALookupSeesASuppliedInstanceOnlyThroughWhatItsModuleDeclares pins that the declaration is
// the one authority for a supplied instance too.
func TestALookupSeesASuppliedInstanceOnlyThroughWhatItsModuleDeclares(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("cache-mod", (*Cache)(nil)))
	reg.Supply(&box{id: "host"})

	if _, err := core.Resolve[Cache](reg); err != nil {
		t.Fatalf("Resolve[Cache] returned %v, want the supplied instance", err)
	}
	if _, err := core.Resolve[Store](reg); !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("Resolve[Store] returned %v, want ErrMissingProvider: the module never declared it", err)
	}
	if got := core.ResolveAll[Greeter](reg); len(got) != 0 {
		t.Fatalf("ResolveAll[Greeter] returned %v, want nothing: the module never declared it", got)
	}
}

// TestSupplyAddsNoModule checks that a supply adds no module and leaves the registered descriptor
// intact.
func TestSupplyAddsNoModule(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("cache-mod", (*Cache)(nil)), placeholder("other", (*Reader)(nil)))

	reg.Supply(&box{id: "host"})

	var names []string
	for _, m := range reg.Modules() {
		names = append(names, m.Name)
	}
	slices.Sort(names)
	if want := []string{"cache-mod", "other"}; !slices.Equal(names, want) {
		t.Fatalf("Modules lists %v after a supply, want the registered modules %v", names, want)
	}
	m, ok := reg.Lookup("cache-mod")
	if !ok || len(m.Provides) != 1 {
		t.Fatalf("Lookup returned %+v/%v for the module that owns the instance, want its descriptor", m, ok)
	}
}

type (
	plainBox struct{ id string }
	heldBox  struct{ items []string }
	voidBox  struct{}
	mapBox   map[string]string
	chanBox  chan struct{}
	sliceBox []string
	funcBox  func()
)

func (b plainBox) CacheID() string { return b.id }
func (b plainBox) StoreID() string { return b.id }
func (b heldBox) CacheID() string  { return strings.Join(b.items, ",") }
func (b heldBox) StoreID() string  { return strings.Join(b.items, ",") }
func (voidBox) CacheID() string    { return "void" }
func (voidBox) StoreID() string    { return "void" }
func (m mapBox) CacheID() string   { return m["k"] }
func (m mapBox) StoreID() string   { return m["k"] }
func (chanBox) CacheID() string    { return "chan" }
func (chanBox) StoreID() string    { return "chan" }
func (s sliceBox) CacheID() string { return strings.Join(s, ",") }
func (s sliceBox) StoreID() string { return strings.Join(s, ",") }
func (funcBox) CacheID() string    { return "func" }
func (funcBox) StoreID() string    { return "func" }

type innerBox struct{ id string }

func (b *innerBox) StoreID() string { return b.id }

type outerBox struct {
	inner innerBox
	id    string
}

func (b *outerBox) CacheID() string { return b.id }
