package core

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func exclusive(tok Token) Provision { return Provision{Token: tok, Exclusive: true} }

func shared(tok Token) Provision { return Provision{Token: tok} }

func stated(name string, state EnablementState, reason string, provs ...Provision) stance {
	return stance{
		module: Module{Name: name, Provides: provs},
		state:  Enablement{State: state, Reason: reason},
	}
}

func mustResolve(t *testing.T, stances ...stance) map[string]Enablement {
	t.Helper()
	final, err := resolveEnablement(stances)
	if err != nil {
		t.Fatalf("resolution failed: %v", err)
	}
	return final
}

func assertEnabled(t *testing.T, final map[string]Enablement, names ...string) {
	t.Helper()
	for _, name := range names {
		if got := final[name]; got.State != StateEnabled {
			t.Errorf("module %q ended as %+v, want enabled", name, got)
		}
	}
}

func assertDisabled(t *testing.T, final map[string]Enablement, name, reasonFragment string) {
	t.Helper()
	got := final[name]
	if got.State != StateDisabled {
		t.Fatalf("module %q ended as %+v, want disabled", name, got)
	}
	if !strings.Contains(got.Reason, reasonFragment) {
		t.Fatalf("module %q was disabled with reason %q, which does not mention %q", name, got.Reason, reasonFragment)
	}
}

// TestBothExclusiveAutoViolates checks that two auto modules claiming one capability exclusively
// fail as ErrExclusiveViolated naming both.
func TestBothExclusiveAutoViolates(t *testing.T) {
	_, err := resolveEnablement([]stance{
		stated("memory", StateAuto, "", exclusive((*cacheCap)(nil))),
		stated("remote", StateAuto, "", exclusive((*cacheCap)(nil))),
	})
	if !errors.Is(err, ErrExclusiveViolated) {
		t.Fatalf("resolution returned %v, want ErrExclusiveViolated", err)
	}
	for _, name := range []string{"memory", "remote"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

// TestAnExclusiveConflictWithoutASuppliedPartySaysNothingOfSupply checks the exact message of an
// exclusive conflict between two constructed modules.
func TestAnExclusiveConflictWithoutASuppliedPartySaysNothingOfSupply(t *testing.T) {
	_, err := resolveEnablement([]stance{
		stated("memory", StateAuto, "", exclusive((*cacheCap)(nil))),
		stated("remote", StateAuto, "", exclusive((*cacheCap)(nil))),
	})

	want := ErrExclusiveViolated.Error() + ": capability " + typeName(reflect.TypeFor[cacheCap]()) +
		` is still delivered by "memory" and "remote" after resolution, while "memory" and "remote" ` +
		"claims it exclusively. Disable all but one provider, or have the default implementation " +
		"declare the capability Exclusive so it stands down when another provider is explicitly enabled"
	if err == nil || err.Error() != want {
		t.Fatalf("resolution returned %v, want the failure to read\n%s", err, want)
	}
}

func suppliedStance(name string, provs ...Provision) stance {
	s := stated(name, StateEnabled, "", provs...)
	s.supplied = true
	return s
}

// TestAnExclusiveConflictSaysWhichPartyHasASuppliedInstance pins what the failure adds for a party
// whose instance the host supplied.
func TestAnExclusiveConflictSaysWhichPartyHasASuppliedInstance(t *testing.T) {
	for _, c := range []struct {
		name    string
		stances []stance
		says    []string
		omits   []string
	}{
		{
			name: "one party",
			stances: []stance{
				suppliedStance("memory", exclusive((*cacheCap)(nil))),
				stated("remote", StateEnabled, "", exclusive((*cacheCap)(nil))),
			},
			says: []string{
				`"memory" has a supplied instance`, "fixes the stance of its module at StateEnabled",
				"stop supplying an instance",
			},
			omits: []string{`"remote" has`, `"remote" each`},
		},
		{
			name: "the party that does not claim the capability",
			stances: []stance{
				suppliedStance("memory", shared((*cacheCap)(nil))),
				stated("remote", StateEnabled, "", exclusive((*cacheCap)(nil))),
			},
			says:  []string{`"memory" has a supplied instance`, "stop supplying an instance"},
			omits: []string{`"remote" has`, `"remote" each`},
		},
		{
			name: "both parties",
			stances: []stance{
				suppliedStance("memory", exclusive((*cacheCap)(nil))),
				suppliedStance("remote", exclusive((*cacheCap)(nil))),
			},
			says: []string{`"memory" and "remote" each have a supplied instance`, "stop supplying an instance"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolveEnablement(c.stances)

			if !errors.Is(err, ErrExclusiveViolated) {
				t.Fatalf("resolution returned %v, want ErrExclusiveViolated", err)
			}
			for _, want := range c.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not say %q", err, want)
				}
			}
			for _, wrong := range c.omits {
				if strings.Contains(err.Error(), wrong) {
					t.Errorf("error %q says %q of a party with no supplied instance", err, wrong)
				}
			}
		})
	}
}

// TestExclusiveAutoStandsDownForExplicitProvider checks that an auto exclusive provider stands down
// when another provider is explicitly enabled.
func TestExclusiveAutoStandsDownForExplicitProvider(t *testing.T) {
	final := mustResolve(t,
		stated("memory", StateAuto, "", exclusive((*cacheCap)(nil))),
		stated("remote", StateEnabled, "", exclusive((*cacheCap)(nil))),
	)
	assertDisabled(t, final, "memory", "already has an explicitly enabled provider")
	assertEnabled(t, final, "remote")
}

// TestPropagationLeavesNonExclusiveProvidersCoexisting checks that shared providers keep running
// while an exclusive auto one stands down.
func TestPropagationLeavesNonExclusiveProvidersCoexisting(t *testing.T) {
	final := mustResolve(t,
		stated("console", StateAuto, "", exclusive((*storeCap)(nil))),
		stated("mail", StateAuto, "", shared((*storeCap)(nil))),
		stated("sms", StateEnabled, "", shared((*storeCap)(nil))),
	)
	assertDisabled(t, final, "console", "already has an explicitly enabled provider")
	assertEnabled(t, final, "mail", "sms")
}

// TestNonExclusiveDefaultAgainstExclusiveExplicitFails checks that a shared auto provider beside an
// exclusive explicit one violates exclusivity.
func TestNonExclusiveDefaultAgainstExclusiveExplicitFails(t *testing.T) {
	_, err := resolveEnablement([]stance{
		stated("memory", StateAuto, "", shared((*cacheCap)(nil))),
		stated("remote", StateEnabled, "", exclusive((*cacheCap)(nil))),
	})
	if !errors.Is(err, ErrExclusiveViolated) {
		t.Fatalf("resolution returned %v, want ErrExclusiveViolated", err)
	}
}

// TestStatedDisabledIsHonoured checks that a module that states disabled stays disabled with its
// own reason.
func TestStatedDisabledIsHonoured(t *testing.T) {
	final := mustResolve(t,
		stated("running", StateAuto, ""),
		stated("switched-off", StateDisabled, "GitHub app credentials not configured"),
	)
	assertDisabled(t, final, "switched-off", "GitHub app credentials not configured")
	assertEnabled(t, final, "running")
}

// TestStatedDisabledWithoutAReasonFails pins the one thing a self-disabled stance owes the
// mechanism: the reason.
func TestStatedDisabledWithoutAReasonFails(t *testing.T) {
	_, err := resolveEnablement([]stance{
		stated("silent", StateDisabled, ""),
	})
	if !errors.Is(err, ErrDisabledWithoutReason) {
		t.Fatalf("resolution returned %v, want ErrDisabledWithoutReason", err)
	}
	if !strings.Contains(err.Error(), "silent") {
		t.Fatalf("error %q does not name the module", err)
	}
}

// TestStatedDisabledDeliversNothingToResolution checks that a disabled exclusive claimant delivers
// nothing, so another auto provider runs.
func TestStatedDisabledDeliversNothingToResolution(t *testing.T) {
	final := mustResolve(t,
		stated("off", StateDisabled, "not configured", exclusive((*cacheCap)(nil))),
		stated("on", StateAuto, "", exclusive((*cacheCap)(nil))),
	)
	assertDisabled(t, final, "off", "not configured")
	assertEnabled(t, final, "on")
}

// TestStatedDisabledIsNotStoodDownForAnotherProvider checks that a module that stated disabled
// keeps its own reason rather than a stand-down caused by another provider.
func TestStatedDisabledIsNotStoodDownForAnotherProvider(t *testing.T) {
	final := mustResolve(t,
		stated("off", StateDisabled, "not configured", exclusive((*cacheCap)(nil))),
		stated("remote", StateEnabled, "", exclusive((*cacheCap)(nil))),
	)
	assertDisabled(t, final, "off", "not configured")
	assertEnabled(t, final, "remote")
}

// TestAbsentPrepareIsAuto pins the zero value: a module without a Prepare callback states
// StateAuto, so it stands down like any other exclusive default.
func TestAbsentPrepareIsAuto(t *testing.T) {
	if (Enablement{}).State != StateAuto {
		t.Fatal("the zero Enablement is not StateAuto")
	}
	final := mustResolve(t,
		stance{module: Module{Name: "silent-default", Provides: []Provision{exclusive((*cacheCap)(nil))}}},
		stated("remote", StateEnabled, "", exclusive((*cacheCap)(nil))),
	)
	assertDisabled(t, final, "silent-default", "already has an explicitly enabled provider")
	assertEnabled(t, final, "remote")
}

// TestResolutionIndependentOfRegistrationOrder checks that every permutation of the stances
// resolves to the same verdicts.
func TestResolutionIndependentOfRegistrationOrder(t *testing.T) {
	base := []stance{
		stated("console", StateAuto, "", exclusive((*storeCap)(nil))),
		stated("mail", StateEnabled, "", shared((*storeCap)(nil))),
		stated("sms", StateEnabled, "", shared((*storeCap)(nil))),
	}
	want := mustResolve(t, base...)

	orders := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, order := range orders {
		permuted := make([]stance, 0, len(base))
		for _, i := range order {
			permuted = append(permuted, base[i])
		}
		got, err := resolveEnablement(permuted)
		if err != nil {
			t.Fatalf("order %v: resolution failed: %v", order, err)
		}
		if !maps.Equal(got, want) {
			t.Fatalf("order %v produced %v, want %v", order, got, want)
		}
	}
}

// TestModuleWithoutProvisionsOnlyUsesOwnState checks that a module delivering and requiring nothing
// keeps its own stance whatever the others do.
func TestModuleWithoutProvisionsOnlyUsesOwnState(t *testing.T) {
	final := mustResolve(t,
		stated("plain", StateAuto, ""),
		stated("remote", StateEnabled, "", exclusive((*cacheCap)(nil))),
	)
	assertEnabled(t, final, "plain", "remote")
}

// TestExclusiveClaimLapsesWhenPropagationDisablesTheClaimant checks that a claim on a second
// capability lapses when propagation disables the module.
func TestExclusiveClaimLapsesWhenPropagationDisablesTheClaimant(t *testing.T) {
	final := mustResolve(t,
		stated("x", StateAuto, "", exclusive((*cacheCap)(nil)), exclusive((*storeCap)(nil))),
		stated("y", StateEnabled, "", exclusive((*cacheCap)(nil))),
		stated("z", StateEnabled, "", shared((*storeCap)(nil))),
	)
	assertDisabled(t, final, "x", "already has an explicitly enabled provider")
	assertEnabled(t, final, "y", "z")
}

// TestResolutionCoversEveryStance checks that resolution reports a verdict for every module in the
// set.
func TestResolutionCoversEveryStance(t *testing.T) {
	final := mustResolve(t,
		stated("a", StateAuto, ""),
		stated("b", StateEnabled, ""),
		stated("c", StateAuto, "", exclusive((*cacheCap)(nil))),
	)
	got := slices.Sorted(maps.Keys(final))
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Fatalf("resolution reported on %v, want %v", got, want)
	}
}

// TestExclusiveResolutionGroupsByTypeIdentity checks that exclusivity is grouped by the capability's
// type identity, so two different interfaces do not conflict.
func TestExclusiveResolutionGroupsByTypeIdentity(t *testing.T) {
	final := mustResolve(t,
		stated("super", StateAuto, "", exclusive((*superCacheCap)(nil))),
		stated("plain", StateAuto, "", exclusive((*cacheCap)(nil))),
	)
	assertEnabled(t, final, "super", "plain")
}
