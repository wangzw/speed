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

// Cache is a capability fixture the black-box suites in this file and in
// run_test.go declare structurally.
type Cache interface {
	// CacheID satisfies the tests' Cache capability.
	CacheID() string
}

// Store is a second capability the registry cases declare structurally.
type Store interface {
	// StoreID satisfies the tests' Store capability.
	StoreID() string
}

// Greeter is a capability the registry cases declare structurally.
type Greeter interface {
	// Greet satisfies the tests' Greeter capability.
	Greet() string
}

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

// TestRegisterDuplicateNamePanicsNamingBothPackagePaths checks that registering a name twice
// panics, naming the module and both registration sites.
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

// TestRegisterRefusesAnEmptyName checks that registering a module with no name panics, naming the
// site, and records nothing.
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

// TestRegisterNamesTheRegistrationPackageWhateverFunctionRegisters checks that the panic naming the
// registration site reports the package of whichever function called Register.
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

// TestRegisterAfterTheDriveStartedPanics pins that the descriptors are fixed before the drive.
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

// TestRegisterDoesNotReviewWhatWasSupplied pins that the owner of a supplied instance is decided
// once, by the call that supplied it.
func TestRegisterDoesNotReviewWhatWasSupplied(t *testing.T) {
	reg := core.New()
	register(reg, placeholder("first", (*Cache)(nil)))
	reg.Supply(&box{id: "host"})

	reg.Register(placeholder("second", (*Cache)(nil)))

	if _, ok := reg.Lookup("second"); !ok {
		t.Fatal("a module declaring a capability that a supplied instance delivers was not registered")
	}
}

// TestRegisterPanicsOnConcreteProvisionToken checks that a provision token pointing to a concrete
// type panics and the module is not registered.
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

// TestRegisterPanicsOnUntypedNilRequirementToken checks that a requirement token given as an
// untyped nil panics, naming the untyped nil.
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

// TestNewRegistryDoesNotInheritProcessRegistrations pins the property that makes a hand-built
// registry the way to assemble a small group of modules.
func TestNewRegistryDoesNotInheritProcessRegistrations(t *testing.T) {
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

// TestLookup checks that a registered module is returned by name and an unknown
// name is not.
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

// TestModulesReturnsEveryRegistration checks that Modules returns every registered module,
// whatever order they were registered in.
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
