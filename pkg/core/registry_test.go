package core_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/core/internal/regprobe"
)

// The capability fixtures for the black-box suites in this file and in
// run_test.go.
type Cache interface{ CacheID() string }

type Store interface{ StoreID() string }

type Greeter interface{ Greet() string }

type box struct{ id string }

func (b *box) CacheID() string { return b.id }
func (b *box) StoreID() string { return b.id }
func (b *box) Greet() string   { return "hello from " + b.id }

func recoverMessage(t *testing.T, fn func()) string {
	t.Helper()
	var msg string
	func() {
		defer func() {
			if r := recover(); r != nil {
				msg = fmt.Sprint(r)
			}
		}()
		fn()
	}()
	if msg == "" {
		t.Fatal("expected a panic, got none")
	}
	return msg
}

// TestRegisterDuplicateNamePanicsNamingBothPackagePaths pins the requirement
// that the panic locates both sides: the available set is decided by imports,
// so the other registrant may sit in a dependency rather than in the host's
// own code.
func TestRegisterDuplicateNamePanicsNamingBothPackagePaths(t *testing.T) {
	reg := core.New()
	regprobe.Register(reg, core.Module{Name: "duplicated"})

	msg := recoverMessage(t, func() {
		reg.Register(core.Module{Name: "duplicated"})
	})

	if !strings.Contains(msg, "duplicated") {
		t.Errorf("panic %q does not name the module", msg)
	}
	for _, want := range []string{regprobeSite, testSite} {
		if !strings.Contains(msg, want) {
			t.Errorf("panic %q does not name the registration site %s", msg, want)
		}
	}
}

// TestRegisterRefusesAnEmptyName pins what an empty module name given to Supply
// rests on: no module is registered under the empty string, so that name never
// finds one and the call is refused rather than falling back to claiming.
func TestRegisterRefusesAnEmptyName(t *testing.T) {
	reg := core.New()

	msg := recoverMessage(t, func() {
		reg.Register(core.Module{Provides: []core.Provision{{Token: (*Cache)(nil)}}})
	})

	assertMentions(t, msg, testSite, "empty name")
	if got := reg.Modules(); len(got) != 0 {
		t.Errorf("a module with an empty name was recorded: %v", got)
	}
}

// TestRegisterNamesTheRegistrationPackageWhateverFunctionRegisters pins that the
// package a registration is attributed to is the package path the package is
// imported under, however the call that registers is written: from a function,
// from a method or from a function literal, in a package whose path ends in an
// ordinary element and in one whose path ends in an element that contains a
// dot. The linker escapes that dot in the symbol names of the package's
// functions, and what a panic names is where a host looks for the module.
func TestRegisterNamesTheRegistrationPackageWhateverFunctionRegisters(t *testing.T) {
	for _, route := range registrationRoutes() {
		t.Run(route.name, func(t *testing.T) {
			reg := core.New()
			route.register(reg, core.Module{Name: "duplicated"})

			msg := recoverMessage(t, func() { reg.Register(core.Module{Name: "duplicated"}) })

			assertMentions(t, msg, "duplicated", route.site, testSite)
		})
	}
}

// TestRegisterAfterTheDriveStartedPanics pins that the descriptors are fixed
// before the drive. A module registered once it began, from a stage, from the
// action or after it returned, would change what Modules and Resources answer
// under the run that asked, and Supply is held to the same boundary. The call
// below is accepted on a registry nothing has driven.
func TestRegisterAfterTheDriveStartedPanics(t *testing.T) {
	late := core.Module{Name: "late"}
	core.New().Register(late)

	for _, c := range []struct {
		name  string
		drive func(reg *core.Registry) error
	}{
		{"from Prepare", func(reg *core.Registry) error {
			reg.Register(core.Module{
				Name: "caller",
				Prepare: func(_ context.Context, r *core.Registry) (core.Enablement, error) {
					r.Register(late)
					return core.Enablement{}, nil
				},
			})
			return reg.Drive(t.Context(), core.Drive{Depth: core.StagePrepare, Participants: []string{"caller"}})
		}},
		{"from New", func(reg *core.Registry) error {
			reg.Register(core.Module{
				Name: "caller",
				New: func(_ context.Context, r *core.Registry) (any, error) {
					r.Register(late)
					return nil, nil
				},
			})
			return reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: []string{"caller"}})
		}},
		{"from the action", func(reg *core.Registry) error {
			return reg.Drive(t.Context(), core.Drive{
				Action: func(_ context.Context, r *core.Registry) error {
					r.Register(late)
					return nil
				},
			})
		}},
		{"after the drive returned", func(reg *core.Registry) error {
			if err := reg.Drive(t.Context(), core.Drive{}); err != nil {
				return err
			}
			reg.Register(late)
			return nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			reg := core.New()

			msg := recoverMessage(t, func() { _ = c.drive(reg) })

			assertMentions(t, msg, testSite, "late")
			if !strings.Contains(strings.ToLower(msg), "drive") {
				t.Errorf("panic %q does not say the call came after the drive started", msg)
			}
			if _, ok := reg.Lookup("late"); ok {
				t.Error("a module registered after the drive started was recorded")
			}
		})
	}
}

// TestRegisterDoesNotReviewWhatWasSupplied pins that the owner of a supplied
// instance is decided once, by the call that supplied it. A module registered
// afterwards declares what it likes, a capability the instance already
// delivers included, and registration neither refuses it nor weighs it
// against the instance.
func TestRegisterDoesNotReviewWhatWasSupplied(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("first", (*Cache)(nil)))
	reg.Supply(&box{id: "host"})

	reg.Register(placeholder("second", (*Cache)(nil)))

	if _, ok := reg.Lookup("second"); !ok {
		t.Fatal("a module declaring a capability that a supplied instance delivers was not registered")
	}
}

func TestRegisterPanicsOnConcreteProvisionToken(t *testing.T) {
	reg := core.New()
	msg := recoverMessage(t, func() {
		reg.Register(core.Module{
			Name:     "concrete",
			Provides: []core.Provision{{Token: (*bytes.Buffer)(nil)}},
		})
	})
	if !strings.Contains(msg, "must point to an interface") {
		t.Fatalf("panic %q does not explain that a capability must be an interface", msg)
	}
	if _, ok := reg.Lookup("concrete"); ok {
		t.Error("a module with an illegal token must not be registered")
	}
}

func TestRegisterPanicsOnUntypedNilRequirementToken(t *testing.T) {
	reg := core.New()
	msg := recoverMessage(t, func() {
		reg.Register(core.Module{
			Name:     "untyped",
			Requires: []core.Requirement{{Token: Cache(nil)}},
		})
	})
	if !strings.Contains(msg, "untyped nil") {
		t.Fatalf("panic %q does not explain that the token is an untyped nil", msg)
	}
}

// TestNewRegistryDoesNotInheritProcessRegistrations pins the property that
// makes a hand-built registry the way to assemble a small group of modules.
func TestNewRegistryDoesNotInheritProcessRegistrations(t *testing.T) {
	// Registering only when absent keeps the test idempotent under -count>1:
	// the process registry outlives an individual test run.
	if _, ok := core.ProcessRegistry.Lookup("registry_test.process-probe"); !ok {
		core.ProcessRegistry.Register(core.Module{Name: "registry_test.process-probe"})
	}
	if _, ok := core.ProcessRegistry.Lookup("registry_test.process-probe"); !ok {
		t.Fatal("the process registry did not record the registration")
	}
	if _, ok := core.New().Lookup("registry_test.process-probe"); ok {
		t.Fatal("a registry from New inherited a process-level registration")
	}
}

func TestLookup(t *testing.T) {
	reg := core.New()
	reg.Register(core.Module{Name: "present", Provides: []core.Provision{{Token: (*Cache)(nil)}}})

	m, ok := reg.Lookup("present")
	if !ok {
		t.Fatal("Lookup missed a registered module")
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Lookup returned %d provisions, want 1", len(m.Provides))
	}
	if _, ok := reg.Lookup("absent"); ok {
		t.Fatal("Lookup found a module that was never registered")
	}
}

// TestModulesReturnsEveryRegistration compares as a set: the design pins a
// name order on ResolveAll and Resources only, and Modules promises none.
func TestModulesReturnsEveryRegistration(t *testing.T) {
	reg := core.New()
	for _, name := range []string{"zeta", "alpha", "mid"} {
		reg.Register(core.Module{Name: name})
	}

	var got []string
	for _, m := range reg.Modules() {
		got = append(got, m.Name)
	}
	slices.Sort(got)
	if want := []string{"alpha", "mid", "zeta"}; !slices.Equal(got, want) {
		t.Fatalf("Modules returned %v, want the set %v", got, want)
	}
}
