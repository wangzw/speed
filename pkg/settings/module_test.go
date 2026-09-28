package settings

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// readingInInit is a module that reads a setting in its own Init, and keeps
// what the read returned for the case to inspect.
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

// A read made before this module's Init has loaded the snapshot is refused,
// and one made by a module ordered after it gets the table's value.
//
// Before the load the snapshot holds nothing, so a read would answer with the
// declared default whatever the table holds, and nothing would say so. A
// module that reads in its own Init without declaring the requirement lands
// wherever the construction order puts it — ahead of this one whenever its
// name sorts first — and the value it acts on for the rest of the process
// would be the default.
func TestAReadBeforeTheLoadIsRefused(t *testing.T) {
	handle := open(t)
	if err := handle.Exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) "+
		"VALUES ('', 'demo.limit', '7', 1, ?)", time.Now().UTC()).Error; err != nil {
		t.Fatalf("planting a row: %v", err)
	}
	reg := core.New()
	reg.Supply((*config.Reader)(nil), noConfig{})
	reg.Register(providingDB(handle))
	reg.Register(providingBus(newRecordingBus()))
	reg.Register(declarerNamed("test.declarer", demoNamespace))
	reg.Register(Module())

	// "crm" sorts ahead of "settings" and declares nothing, so it is
	// initialised first; "crm.after" declares the requirement.
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

// A store that has not loaded refuses a read rather than answering from the
// declared default, and the refusal names the key and what to do about it.
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

// eventsOnly is the configuration a host that relies on change events alone
// supplies: the periodic refresh is off.
type eventsOnly struct{}

func (eventsOnly) Decode(namespace string, target any) error {
	if cfg, ok := target.(*moduleConfig); ok && namespace == ConfigNamespace {
		cfg.RefreshInterval = 0
	}
	return nil
}

// A change made between this module's Init and its Start is served once the
// module has started, even though no event for it ever arrives.
//
// A transport bus creates its consumers when it starts, and hands them only
// what is published from then on, while this module loads its snapshot in
// Init, before any Start. A change another replica published in between is
// therefore delivered to nobody here, and with the refresh off this replica
// would serve the value from before it until a restart. This module requires
// the bus, so it starts after it: a load at its own Start sees everything
// written before the consumers existed.
func TestAChangeBeforeTheBusStartsIsNotLost(t *testing.T) {
	handle := open(t)
	reg := core.New()
	reg.Supply((*config.Reader)(nil), eventsOnly{})
	reg.Register(providingDB(handle))
	// The recording bus delivers nothing unless a case drives it, which is
	// what a transport bus does with an event published before it started.
	reg.Register(providingBus(newRecordingBus()))
	reg.Register(declarerNamed("test.declarer", demoNamespace))
	reg.Register(Module())
	// Initialised after this module, and before any module starts: the row it
	// writes is the one another replica writes in that window.
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
