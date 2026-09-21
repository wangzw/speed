package settings

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

// noConfig is the configuration reader a host supplies. It decodes nothing,
// which leaves every declared default in place.
type noConfig struct{}

func (noConfig) Decode(string, any) error { return nil }

// providingDB delivers the database capability over a real handle.
func providingDB(handle *gorm.DB) core.Module {
	return core.Module{
		Name:     "test.database",
		Provides: []core.Provision{{Token: (*db.Database)(nil), Exclusive: true}},
		New: func(context.Context, *core.Registry) (any, error) {
			return stubDatabase{handle: handle, dialect: db.SQLite}, nil
		},
	}
}

// providingBus delivers the event capability.
func providingBus(bus event.Bus) core.Module {
	return core.Module{
		Name:     "test.event",
		Provides: []core.Provision{{Token: (*event.Bus)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return bus, nil },
	}
}

// contestedCapability is the token the two declarers below claim. Nothing ever
// resolves it: the exclusive claim alone is what makes resolution run over
// them.
type contestedCapability interface{ contested() }

// contesting is a declarer that claims one capability exclusively, stating
// whether it is the provider the host explicitly enabled. Two of these on the
// same claim are what puts one through resolution and leaves it with the
// registry's StateDisabled verdict — the one way a participating module now
// ends a run without running.
func contesting(name, namespace string, explicit bool) core.Module {
	defaults := demoDefaults()
	m := declaring(name, Schema{Namespace: namespace, Mounts: []Mount{{Value: &defaults}}})
	m.Provides = []core.Provision{{Token: (*contestedCapability)(nil), Exclusive: true}}
	m.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		if explicit {
			return core.Enablement{State: core.StateEnabled}, nil
		}
		return core.Enablement{State: core.StateAuto}, nil
	}
	return m
}

// declarerNamed is an ordinary declarer in a namespace of its own.
func declarerNamed(name, namespace string) core.Module {
	defaults := demoDefaults()
	return declaring(name, Schema{Namespace: namespace, Mounts: []Mount{{Value: &defaults}}})
}

// The module takes part in the lifecycle: it constructs, loads, serves reads
// and writes, and subscribes to its own change type.
func TestTheModuleDrivesAndServes(t *testing.T) {
	handle := open(t)
	bus := newRecordingBus()
	reg := core.New()
	reg.Supply((*config.Reader)(nil), noConfig{})
	reg.Register(providingDB(handle))
	reg.Register(providingBus(bus))
	reg.Register(declarerNamed("test.declarer", demoNamespace))
	reg.Register(Module())

	ctx := context.Background()
	err := reg.Drive(ctx, core.Drive{
		Depth:        core.StageStart,
		Participants: []string{"test.database", "test.event", "test.declarer", ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			store, err := core.Resolve[Store](reg)
			if err != nil {
				t.Fatalf("resolving the settings capability: %v", err)
			}
			if err := store.Set(ctx, Write{Layer: ForTenant("t1"), Key: "demo.limit", Value: 12}); err != nil {
				t.Fatalf("writing through the resolved capability: %v", err)
			}
			if got, err := Get[int](store, ForTenant("t1"), "demo.limit"); err != nil || got != 12 {
				t.Errorf("read back %v (%v), want 12", got, err)
			}
			if entries, err := store.List(ctx, ForTenant("t1")); err != nil || len(entries) != 1 {
				t.Errorf("List returned %+v (%v), want one row", entries, err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the lifecycle: %v", err)
	}

	bus.mu.Lock()
	handlers := len(bus.handlers[ChangedEventType])
	bus.mu.Unlock()
	if handlers == 0 {
		t.Errorf("the module did not subscribe to %q", ChangedEventType)
	}
}

// A key's validity follows the binary rather than this run's verdict on its
// module. The key table is the available set and no consumer point subtracts
// from it (architecture invariant 15, design-settings.md), so a module that
// lost a resolution keeps its keys exactly as one the command never took in
// does: a subtraction would make the same key legal under one command and
// illegal under another while the declaration itself did not change.
//
// The resolution loser is the case that used to be subtracted here. Reading
// the verdict back first is what keeps this a test of that case: if the drive
// stopped disabling the loser, the assertion would hold for the wrong reason.
func TestEveryRegisteredDeclarationStaysInTheKeyTable(t *testing.T) {
	handle := open(t)
	reg := core.New()
	reg.Supply((*config.Reader)(nil), noConfig{})
	reg.Register(providingDB(handle))
	reg.Register(providingBus(newRecordingBus()))
	reg.Register(contesting("test.loser", "loser", false))
	reg.Register(contesting("test.winner", "winner", true))
	reg.Register(declarerNamed("test.out", "out"))
	reg.Register(Module())

	ctx := context.Background()
	err := reg.Drive(ctx, core.Drive{
		// To Prepare and no further: the verdicts are what this case reads, and
		// the contesting declarers stand in for modules rather than delivering a
		// capability anything resolves.
		Depth: core.StagePrepare,
		// test.out is registered but named by no drive: this run never took it in.
		Participants: []string{"test.database", "test.event", "test.loser", "test.winner", ModuleName},
	})
	if err != nil {
		t.Fatalf("driving to Prepare: %v", err)
	}
	lost, known := reg.Enablement("test.loser")
	if !known || lost.State != core.StateDisabled {
		t.Fatalf("test.loser is %v (known = %v), want the StateDisabled resolution gives the "+
			"provider that stood down: without it this case says nothing about a resolution loser",
			lost.State, known)
	}

	keys, err := buildKeyTable(reg)
	if err != nil {
		t.Fatalf("building the key table: %v", err)
	}
	cases := []struct {
		key  string
		want bool
	}{
		{"loser.limit", true},  // took part and lost the resolution
		{"winner.limit", true}, // took part and won it
		{"out.limit", true},    // never took part: no verdict to honour
	}
	for _, c := range cases {
		if _, found := keys.lookup(c.key); found != c.want {
			t.Errorf("key %q present = %v, want %v", c.key, found, c.want)
		}
	}
}
