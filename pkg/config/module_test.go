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

// TestModuleRegistersItselfWithTheProcessRegistry pins that importing the
// package is all a host does: the registration happens in init, so the host
// writes neither the descriptor nor a Register call, and the tree it starts over
// core.ProcessRegistry finds the module the reader is supplied to.
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

	// What a host does with the process registry. NewApp panics over one that
	// lacks the module, and a panic would end the whole test binary where a
	// failure of this case is what is meant.
	defer func() {
		if raised := recover(); raised != nil {
			t.Errorf("starting a tree over the process registry panicked: %v", raised)
		}
	}()
	NewApp(core.ProcessRegistry)
}

// TestModuleDeclaresOnlyTheDeliveryOfTheReader pins the descriptor.
//
// The reader is built by loading, ahead of the drive, so the module has nothing
// to construct and nothing to judge: a New would let a drive that was never
// handed the reader construct one the run did not load, and a claim of
// Exclusive would keep the dependency closure of a module that requires the
// reader from taking this one in, which a later case observes. The reader holds
// no connection or file, so there is no Stop or Close to run on it either; a
// Serve would make the module an entry point.
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

// requiringTheReader registers the config module and one module that requires
// the reader.
func requiringTheReader() *core.Registry {
	reg := core.New()
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     "consumer",
		Requires: []core.Requirement{{Token: (*Reader)(nil)}},
	})
	return reg
}

// TestAModuleRequiringTheReaderBringsTheConfigModuleIntoItsClosure pins what
// leaving Exclusive off the declaration buys. Naming the one module that
// requires the reader on a command is enough for the config module to be among
// the participants, with no line of the host's own about it.
func TestAModuleRequiringTheReaderBringsTheConfigModuleIntoItsClosure(t *testing.T) {
	got, err := requiringTheReader().Closure([]string{"consumer"})
	if err != nil {
		t.Fatalf("expanding the closure failed: %v", err)
	}
	if want := []string{ModuleName, "consumer"}; !slices.Equal(got, want) {
		t.Fatalf("the closure of the module requiring the reader is %v, want %v", got, want)
	}
}

// TestAReaderNobodySuppliedFailsAsTheConfigModuleHavingNoInstance pins what a
// host that never supplied the reader is told. The module is in the closure of
// whoever requires the reader and is constructed ahead of it; it has no New, so
// the failure is the module's own and says its instance can only come from the
// host.
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
