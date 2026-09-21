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

// standingDown is a declarer that takes part in the run and then states it does
// not run, which is the one way a module negates its own declaration.
func standingDown(name, namespace string) core.Module {
	defaults := demoDefaults()
	m := declaring(name, Schema{Namespace: namespace, Mounts: []Mount{{Value: &defaults}}})
	m.Prepare = func(context.Context, *core.Registry) (core.Enablement, error) {
		return core.Enablement{State: core.StateDisabled, Reason: "not needed by this run"}, nil
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
			if err := store.Set(ctx, Write{Tenant: "t1", Key: "demo.limit", Value: 12}); err != nil {
				t.Fatalf("writing through the resolved capability: %v", err)
			}
			if got, err := Get[int](store, "t1", "demo.limit"); err != nil || got != 12 {
				t.Errorf("read back %v (%v), want 12", got, err)
			}
			if entries, err := store.List(ctx, "t1"); err != nil || len(entries) != 1 {
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

// A declaration made by a module this run did not take in stays in the key
// table: that module never spoke, and the validity of a key follows the binary
// rather than one command's participating set.
//
// A declaration made by a module that did take part and then stated it does
// not run leaves: that is the assembly's own verdict on it.
func TestOnlyARunLevelVerdictRemovesADeclaration(t *testing.T) {
	handle := open(t)
	reg := core.New()
	reg.Supply((*config.Reader)(nil), noConfig{})
	reg.Register(providingDB(handle))
	reg.Register(providingBus(newRecordingBus()))
	reg.Register(standingDown("test.off", "off"))
	reg.Register(declarerNamed("test.on", "on"))
	reg.Register(declarerNamed("test.out", "out"))
	reg.Register(Module())

	ctx := context.Background()
	err := reg.Drive(ctx, core.Drive{
		Depth: core.StageNew,
		// test.out is registered but named by no drive: it stated nothing.
		Participants: []string{"test.database", "test.event", "test.off", "test.on", ModuleName},
	})
	if err != nil {
		t.Fatalf("driving to New: %v", err)
	}

	keys, err := buildKeyTable(reg)
	if err != nil {
		t.Fatalf("building the key table: %v", err)
	}
	cases := []struct {
		key  string
		want bool
	}{
		{"on.limit", true},   // took part and runs
		{"out.limit", true},  // never took part: no verdict to honour
		{"off.limit", false}, // took part and stood down
	}
	for _, c := range cases {
		if _, found := keys.lookup(c.key); found != c.want {
			t.Errorf("key %q present = %v, want %v", c.key, found, c.want)
		}
	}
}
