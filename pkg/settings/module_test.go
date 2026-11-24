package settings

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

type noConfig struct{}

func (noConfig) Decode(string, any) error { return nil }

func providingDB(handle *gorm.DB) core.Module {
	return core.Module{
		Name:     "test.database",
		Provides: []core.Provision{{Token: (*db.Database)(nil), Exclusive: true}},
		New: func(context.Context, *core.Registry) (any, error) {
			return stubDatabase{handle: handle, dialect: db.SQLite}, nil
		},
	}
}

func providingBus(bus event.Bus) core.Module {
	return core.Module{
		Name:     "test.event",
		Provides: []core.Provision{{Token: (*event.Bus)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return bus, nil },
	}
}

type contestedCapability interface{ contested() }

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

func declarerNamed(name, namespace string) core.Module {
	defaults := demoDefaults()
	return declaring(name, Schema{Namespace: namespace, Mounts: []Mount{{Value: &defaults}}})
}

// TestTheModuleDrivesAndServes checks that the module constructs, loads, serves
// reads and writes, and subscribes to its own change type.
func TestTheModuleDrivesAndServes(t *testing.T) {
	handle := open(t)
	bus := newRecordingBus()
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
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
			if !slices.ContainsFunc(store.Declared(), func(d DeclaredItem) bool {
				return d.Key == "demo.limit" && d.Module == "test.declarer" && d.Default == "100"
			}) {
				t.Errorf("Declared does not list test.declarer's demo.limit: %+v", store.Declared())
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

func readingInInit(name string, requires []core.Requirement, got *int, readErr *error) core.Module {
	return core.Module{
		Name:     name,
		Requires: requires,
		New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(_ context.Context, reg *core.Registry, _ any) error {
			store, err := core.Resolve[Store](reg)
			if err != nil {
				return err
			}
			*got, *readErr = Get[int](store, Global(), "demo.limit")
			return nil
		},
	}
}

// TestAReadBeforeTheLoadIsRefused checks that a read before Init is refused and a module ordered after it reads the table value.
func TestAReadBeforeTheLoadIsRefused(t *testing.T) {
	handle := open(t)
	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('', 'demo.limit', '7', 1, ?)", time.Now().UTC()).Error; err != nil {
		t.Fatalf("planting a row: %v", err)
	}
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(providingDB(handle))
	reg.Register(providingBus(newRecordingBus()))
	reg.Register(declarerNamed("test.declarer", demoNamespace))
	reg.Register(Module())

	var early, late int
	var earlyErr, lateErr error
	reg.Register(readingInInit("crm", nil, &early, &earlyErr))
	reg.Register(readingInInit("crm.after", []core.Requirement{{Token: (*Store)(nil)}}, &late, &lateErr))

	err := reg.Drive(context.Background(), core.Drive{
		Depth: core.StageInit,
		Participants: []string{
			"test.database", "test.event", "test.declarer", ModuleName,
			"crm", "crm.after",
		},
	})
	if err != nil {
		t.Fatalf("driving the lifecycle: %v", err)
	}
	if !errors.Is(earlyErr, ErrNotLoaded) {
		t.Errorf("the read ahead of the load returned %v (%v), want ErrNotLoaded", early, earlyErr)
	}
	if lateErr != nil || late != 7 {
		t.Errorf("the read after the load returned %v (%v), want the table's 7", late, lateErr)
	}
}

// TestAStoreThatHasNotLoadedRefusesAReadNamingTheKey checks that an unloaded store refuses a read naming the key.
func TestAStoreThatHasNotLoadedRefusesAReadNamingTheKey(t *testing.T) {
	store := newSettingsStore(demoTable(t), stubDatabase{handle: open(t), dialect: db.SQLite},
		newRecordingBus(), moduleSettings{})
	_, err := Get[int](store, Global(), "demo.limit")
	if !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("a read before the load returned %v, want ErrNotLoaded", err)
	}
	for _, want := range []string{"demo.limit", "Requires"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

type eventsOnly struct{}

func (eventsOnly) Decode(namespace string, target any) error {
	if cfg, ok := target.(*moduleConfig); ok && namespace == ConfigNamespace {
		cfg.RefreshInterval = 0
	}
	return nil
}

// TestAChangeBeforeTheBusStartsIsNotLost checks that a change between Init and Start is served once the module starts.
func TestAChangeBeforeTheBusStartsIsNotLost(t *testing.T) {
	handle := open(t)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(eventsOnly{}, config.ModuleName)
	reg.Register(providingDB(handle))
	reg.Register(providingBus(newRecordingBus()))
	reg.Register(declarerNamed("test.declarer", demoNamespace))
	reg.Register(Module())
	reg.Register(core.Module{
		Name:     "test.other-replica",
		Requires: []core.Requirement{{Token: (*Store)(nil)}},
		New:      func(context.Context, *core.Registry) (any, error) { return struct{}{}, nil },
		Init: func(context.Context, *core.Registry, any) error {
			return handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
				"VALUES ('', 'demo.limit', '7', 1, ?)", time.Now().UTC()).Error
		},
	})

	var got int
	var readErr error
	err := reg.Drive(context.Background(), core.Drive{
		Depth: core.StageStart,
		Participants: []string{
			"test.database", "test.event", "test.declarer", ModuleName, "test.other-replica",
		},
		Action: func(_ context.Context, reg *core.Registry) error {
			store, err := core.Resolve[Store](reg)
			if err != nil {
				return err
			}
			got, readErr = Get[int](store, Global(), "demo.limit")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the lifecycle: %v", err)
	}
	if readErr != nil || got != 7 {
		t.Errorf("after the start the read returned %v (%v), want the 7 written before it", got, readErr)
	}
}

// TestEveryRegisteredDeclarationStaysInTheKeyTable checks that a module that
// lost a resolution keeps its keys, as the available set is not subtracted.
func TestEveryRegisteredDeclarationStaysInTheKeyTable(t *testing.T) {
	handle := open(t)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(providingDB(handle))
	reg.Register(providingBus(newRecordingBus()))
	reg.Register(contesting("test.loser", "loser", false))
	reg.Register(contesting("test.winner", "winner", true))
	reg.Register(declarerNamed("test.out", "out"))
	reg.Register(Module())

	ctx := context.Background()
	err := reg.Drive(ctx, core.Drive{
		Depth:        core.StagePrepare,
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
		{"loser.limit", true},
		{"winner.limit", true}, // took part and won it
		{"out.limit", true},    // never took part: no verdict to honour
	}
	for _, c := range cases {
		if _, found := keys.lookup(c.key); found != c.want {
			t.Errorf("key %q present = %v, want %v", c.key, found, c.want)
		}
	}
}

type suppliedStore struct{ Store }

func driveWithSupplied(t *testing.T, handle *gorm.DB, bus event.Bus, store Store) Store {
	t.Helper()
	reg := core.New()
	reg.Register(providingDB(handle))
	reg.Register(providingBus(bus))
	reg.Register(Module())
	reg.Supply(store, ModuleName)

	var resolved Store
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{"test.database", "test.event"},
		Action: func(_ context.Context, reg *core.Registry) error {
			var err error
			resolved, err = core.Resolve[Store](reg)
			return err
		},
	})
	if err != nil {
		t.Fatalf("driving with a supplied store: %v", err)
	}
	return resolved
}

func subscribersTo(bus *recordingBus, eventType string) int {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	return len(bus.handlers[eventType])
}

// TestAStandInSuppliedForTheModuleIsLeftAlone checks that a supplied store this
// module did not build is resolved and left alone by the callbacks.
func TestAStandInSuppliedForTheModuleIsLeftAlone(t *testing.T) {
	bus := newRecordingBus()
	standIn := &suppliedStore{}

	got := driveWithSupplied(t, open(t), bus, standIn)

	if got != Store(standIn) {
		t.Errorf("the capability resolves to %T, want the supplied stand-in", got)
	}
	if n := subscribersTo(bus, ChangedEventType); n != 0 {
		t.Errorf("%d handlers subscribed to %q for an instance this module did not build, want none",
			n, ChangedEventType)
	}
}

// TestTheModulesOwnStoreSuppliedForItIsSubscribedAndLoaded checks that the
// module's own store supplied by the host is subscribed and loaded.
func TestTheModulesOwnStoreSuppliedForItIsSubscribedAndLoaded(t *testing.T) {
	handle := open(t)
	bus := newRecordingBus()
	own := newSettingsStore(demoTable(t), stubDatabase{handle: handle, dialect: db.SQLite}, bus, moduleSettings{})
	own.logger = discardLogger()

	got := driveWithSupplied(t, handle, bus, own)

	if got != Store(own) {
		t.Errorf("the capability resolves to %T, want the supplied store", got)
	}
	if n := subscribersTo(bus, ChangedEventType); n != 1 {
		t.Errorf("%d handlers subscribed to %q, want the one this module's own store gets", n, ChangedEventType)
	}
	if value, err := Get[int](own, Global(), "demo.limit"); err != nil || value != 100 {
		t.Errorf("read %v (%v) from the supplied store, want the declared 100 of a loaded one", value, err)
	}
}
