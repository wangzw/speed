package config

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

// TestModuleRegistersItselfWithTheProcessRegistry checks that module registers itself with the process registry.
func TestModuleRegistersItselfWithTheProcessRegistry(t *testing.T) {
	registered, ok := core.ProcessRegistry.Lookup(ModuleName)
	if !ok {
		t.Fatalf("importing the package did not register a module named %q with the process registry",
			ModuleName)
	}
	if len(registered.Provides) != 1 ||
		reflect.TypeOf(registered.Provides[0].Token) != reflect.TypeOf((*Reader)(nil)) {
		t.Errorf("the registered module declares %+v, want the delivery of Reader", registered.Provides)
	}

	defer func() {
		if raised := recover(); raised != nil {
			t.Errorf("starting a tree over the process registry panicked: %v", raised)
		}
	}()
	NewApp(core.ProcessRegistry)
}

// TestModuleDeclaresOnlyTheDeliveryOfTheReader checks that module declares only the delivery of the reader.
func TestModuleDeclaresOnlyTheDeliveryOfTheReader(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("the module registers as %q and exports %q as its name", m.Name, ModuleName)
	}
	if m.Name != "config" {
		t.Errorf("the module is named %q, want config: the name is part of what a host writes "+
			"to supply an instance to it or to take it into a command", m.Name)
	}

	if len(m.Provides) != 1 {
		t.Fatalf("the descriptor declares %d capabilities, want exactly one", len(m.Provides))
	}
	if got, want := reflect.TypeOf(m.Provides[0].Token), reflect.TypeOf((*Reader)(nil)); got != want {
		t.Errorf("the delivered token is %v, want %v", got, want)
	}
	if m.Provides[0].Exclusive {
		t.Error("the Reader capability is claimed exclusively, so the dependency closure of a " +
			"module requiring it would not take this module in")
	}
	if len(m.Requires) != 0 {
		t.Errorf("the descriptor requires %v, and the module needs nothing at construction time", m.Requires)
	}
	if len(m.Resources) != 0 {
		t.Errorf("the descriptor declares the resources %v, and the module adds no input item, "+
			"argument or required item of its own", m.Resources)
	}
	for _, callback := range []struct {
		name     string
		declared bool
	}{
		{"Prepare", m.Prepare != nil},
		{"New", m.New != nil},
		{"Migrate", m.Migrate != nil},
		{"Init", m.Init != nil},
		{"Start", m.Start != nil},
		{"Serve", m.Serve != nil},
		{"Stop", m.Stop != nil},
		{"Close", m.Close != nil},
	} {
		if callback.declared {
			t.Errorf("the descriptor declares %s, and the module has no callback", callback.name)
		}
	}
}

func requiringTheReader() *core.Registry {
	reg := core.New()
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     "consumer",
		Requires: []core.Requirement{{Token: (*Reader)(nil)}},
	})
	return reg
}

// TestAModuleRequiringTheReaderBringsTheConfigModuleIntoItsClosure checks that a module requiring the reader brings the config module into its closure.
func TestAModuleRequiringTheReaderBringsTheConfigModuleIntoItsClosure(t *testing.T) {
	got, err := requiringTheReader().Closure([]string{"consumer"})
	if err != nil {
		t.Fatalf("expanding the closure failed: %v", err)
	}
	if want := []string{ModuleName, "consumer"}; !slices.Equal(got, want) {
		t.Fatalf("the closure of the module requiring the reader is %v, want %v", got, want)
	}
}

// TestAReaderNobodySuppliedFailsAsTheConfigModuleHavingNoInstance checks that a reader nobody supplied fails as the config module having no instance.
func TestAReaderNobodySuppliedFailsAsTheConfigModuleHavingNoInstance(t *testing.T) {
	reg := requiringTheReader()
	participants, err := reg.Closure([]string{"consumer"})
	if err != nil {
		t.Fatalf("expanding the closure failed: %v", err)
	}

	err = reg.Drive(t.Context(), core.Drive{Depth: core.StageNew, Participants: participants})
	if !errors.Is(err, core.ErrUndeliveredCapability) {
		t.Fatalf("a drive whose reader nobody supplied returned %v, want %v",
			err, core.ErrUndeliveredCapability)
	}
	for _, want := range []string{strconv.Quote(ModuleName), "no New callback", "Supply"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure reads %q, which does not say %q", err, want)
		}
	}
}
