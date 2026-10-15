package static

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/settings"
)

// relaxBus is a bus that keeps the handlers subscribed and delivers to them, the
// way the process-local bus does, and remembers how many handlers each event
// type carries.
type relaxBus struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
}

func newRelaxBus() *relaxBus { return &relaxBus{handlers: map[string][]event.Handler{}} }

func (b *relaxBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *relaxBus) Publish(context.Context, event.Event) error { return nil }

// deliver hands an event to the handlers of its type, as the bus would.
func (b *relaxBus) deliver(ctx context.Context, evt event.Event) {
	b.mu.Lock()
	handlers := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	for _, h := range handlers {
		_ = h(ctx, evt)
	}
}

// subscribed is how many handlers the type carries.
func (b *relaxBus) subscribed(eventType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.handlers[eventType])
}

// relaxAudit keeps the rows it was asked to write, and fails them when told to.
type relaxAudit struct {
	mu   sync.Mutex
	rows []audit.Entry
	err  error
}

func (a *relaxAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, e)
	return a.err
}

func (a *relaxAudit) entries() []audit.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]audit.Entry(nil), a.rows...)
}

// relaxRig drives the module's subscription the way an assembly drives it: a
// backend holding a settings store, a bus it is subscribed to, an audit seam
// that keeps its rows, and a logger that keeps its lines.
type relaxRig struct {
	b     *backend
	bus   *relaxBus
	audit *relaxAudit
	out   *capture
}

func newRelaxRig(t *testing.T) *relaxRig {
	t.Helper()
	out := &capture{}
	b := tableWith(t, map[string]string{"alice": "secret"})
	b.logger = out.logger()
	b.store = newFakeSettings()
	rec := &relaxAudit{}
	b.audit = rec
	bus := newRelaxBus()
	b.bus = bus
	bus.Subscribe(settings.ChangedEventType, "test", b.onSettingsChanged)
	return &relaxRig{b: b, bus: bus, audit: rec, out: out}
}

// deliver hands a settings change to the module as the bus would when the
// settings module publishes one.
func (r *relaxRig) deliver(change settings.Change) {
	r.bus.deliver(context.Background(), event.Event{Type: settings.ChangedEventType, Payload: change})
}

// warnLines returns the Warn lines the logger kept that carry every fragment.
func warnLines(out *capture, fragments ...string) []string {
	var lines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if !strings.Contains(line, "level=WARN") {
			continue
		}
		holds := true
		for _, fragment := range fragments {
			if !strings.Contains(line, fragment) {
				holds = false
				break
			}
		}
		if holds {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestARelaxingChangeWritesAWarnAndAnAuditRow holds the visibility: each watched
// setting moved in its relaxing direction writes one Warn naming the key and the
// direction, and one best-effort audit row, and nothing else.
func TestARelaxingChangeWritesAWarnAndAnAuditRow(t *testing.T) {
	for _, c := range []struct {
		key, old, new, direction string
	}{
		{freeAttemptsPath, "5", "7", moveUp},
		{failWindowPath, "15m0s", "10m0s", moveDown},
		{maxBackoffPath, "15m0s", "5m0s", moveDown},
	} {
		t.Run(c.key, func(t *testing.T) {
			r := newRelaxRig(t)
			r.deliver(settings.Change{Key: c.key, Old: c.old, New: c.new, Actor: "op"})

			if got := warnLines(r.out, "setting="+c.key, "direction="+c.direction); len(got) != 1 {
				t.Errorf("the Warns naming %s and %s are %d, want 1; the log was:\n%s",
					c.key, c.direction, len(got), r.out.String())
			}
			rows := r.audit.entries()
			if len(rows) != 1 {
				t.Fatalf("the audit holds %d rows, want 1", len(rows))
			}
			if rows[0].Action != relaxedAction || rows[0].Object != c.key || rows[0].Outcome != relaxedOutcome {
				t.Errorf("audit row = %+v, want the relaxed action on %s", rows[0], c.key)
			}
			if rows[0].Detail["change"] != c.direction {
				t.Errorf("the audit detail = %v, want the direction", rows[0].Detail)
			}
		})
	}
}

// TestATighteningChangeWritesNothing holds the other direction: a move that
// checks more, and one that keeps a value the same, is not a relaxation and is
// not reported.
func TestATighteningChangeWritesNothing(t *testing.T) {
	r := newRelaxRig(t)
	r.deliver(settings.Change{Key: freeAttemptsPath, Old: "7", New: "5"})
	r.deliver(settings.Change{Key: failWindowPath, Old: "10m0s", New: "15m0s"})
	r.deliver(settings.Change{Key: maxBackoffPath, Old: "5m0s", New: "15m0s"})
	r.deliver(settings.Change{Key: freeAttemptsPath, Old: "5", New: "5"})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a tightening change wrote audit rows: %+v", rows)
	}
	if got := warnLines(r.out); len(got) != 0 {
		t.Errorf("a tightening change wrote a Warn:\n%s", r.out.String())
	}
}

// TestASensitiveChangeIsSkippedEntirely holds the Sensitive filter against a
// change that would relax: a withheld change is dropped before the relaxation
// test, so it writes no Warn and no audit row.
func TestASensitiveChangeIsSkippedEntirely(t *testing.T) {
	r := newRelaxRig(t)
	r.deliver(settings.Change{Key: freeAttemptsPath, Old: "5", New: "7", Sensitive: true})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("a sensitive change wrote audit rows: %+v", rows)
	}
	if got := warnLines(r.out); len(got) != 0 {
		t.Errorf("a sensitive change wrote a Warn:\n%s", r.out.String())
	}
}

// TestAnUnwatchedKeyIsIgnored holds the filter: a key this module does not watch
// is dropped, and cache-ttl, which has no relaxing direction, is one of them.
func TestAnUnwatchedKeyIsIgnored(t *testing.T) {
	r := newRelaxRig(t)
	r.deliver(settings.Change{Key: cacheTTLPath, Old: "60s", New: "5m0s"})
	r.deliver(settings.Change{Key: "authn.mfa.max-per-kind", Old: "1", New: "20"})
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("an unwatched change wrote audit rows: %+v", rows)
	}
	if got := warnLines(r.out); len(got) != 0 {
		t.Errorf("an unwatched change wrote a Warn:\n%s", r.out.String())
	}
}

// TestALayerThatHeldNoRowReadsAsTheDeclaredDefault holds the comparison against
// a change whose old side holds no row: the declared default is the strict end
// of every watched setting, so raising free-attempts relaxes and lowering it
// does not.
func TestALayerThatHeldNoRowReadsAsTheDeclaredDefault(t *testing.T) {
	t.Run("raising with no old row relaxes", func(t *testing.T) {
		r := newRelaxRig(t)
		r.deliver(settings.Change{Key: freeAttemptsPath, Old: "", New: "7"})
		if got := warnLines(r.out, freeAttemptsPath, moveUp); len(got) != 1 {
			t.Errorf("raising free-attempts from its declared default wrote %d Warns, want 1:\n%s", len(got), r.out.String())
		}
	})
	t.Run("lowering with no old row does not", func(t *testing.T) {
		r := newRelaxRig(t)
		r.deliver(settings.Change{Key: freeAttemptsPath, Old: "", New: "3"})
		if rows := r.audit.entries(); len(rows) != 0 {
			t.Errorf("lowering free-attempts below its declared default wrote audit rows: %+v", rows)
		}
	})
	t.Run("a delete reads as the declared default", func(t *testing.T) {
		// fail-window relaxes downward; deleting a raised row returns it to the
		// default of 15m, which is above the 10m it held, so it tightens.
		r := newRelaxRig(t)
		r.deliver(settings.Change{Key: failWindowPath, Old: "10m0s", New: "", Deleted: true})
		if rows := r.audit.entries(); len(rows) != 0 {
			t.Errorf("deleting a lowered fail-window wrote audit rows: %+v", rows)
		}
	})
}

// TestARelaxedSettingWithoutAnAuditSeamIsAWarnAndOnlyThat holds the best-effort
// direction: with no audit seam a loosening change is still a Warn, no audit row
// is written, and the handler does not panic.
func TestARelaxedSettingWithoutAnAuditSeamIsAWarnAndOnlyThat(t *testing.T) {
	r := newRelaxRig(t)
	r.b.audit = nil
	r.deliver(settings.Change{Key: freeAttemptsPath, Old: "5", New: "7"})
	if got := warnLines(r.out, freeAttemptsPath, moveUp); len(got) != 1 {
		t.Errorf("with no audit seam a relaxing change wrote %d Warns, want 1:\n%s", len(got), r.out.String())
	}
	if rows := r.audit.entries(); len(rows) != 0 {
		t.Errorf("with no audit seam the handler wrote audit rows: %+v", rows)
	}
}

// TestTheModuleSubscribesOnlyWithABusAndAStore holds the wiring: the module
// subscribes to the settings change event only when both the bus that carries it
// and the store it is about are present, and it does not panic when neither is.
func TestTheModuleSubscribesOnlyWithABusAndAStore(t *testing.T) {
	for _, c := range []struct {
		name      string
		bus, stor bool
		want      int
	}{
		{"a bus and a store", true, true, 1},
		{"a bus and no store", true, false, 0},
		{"a store and no bus", false, true, 0},
		{"neither", false, false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			bus := newRelaxBus()
			var store settings.Store
			if c.stor {
				store = newFakeSettings()
			}
			driveWatching(t, bus, c.bus, store)
			if got := bus.subscribed(settings.ChangedEventType); got != c.want {
				t.Errorf("the module subscribed to %s %d times, want %d", settings.ChangedEventType, got, c.want)
			}
		})
	}
}

// driveWatching takes the module through a full assembly holding a bus provider
// when withBus is set and a settings store provider when store is not nil, so a
// case can see whether the module subscribed. The bus is registered regardless
// so a case without one still drives the module over a legal assembly.
func driveWatching(t *testing.T, bus *relaxBus, withBus bool, store settings.Store) {
	t.Helper()
	subjects := `{"alice":"` + cheapEntry(t, "alices") + `"}`
	body := `{"authn":{"static":{"subjects":` + subjects + `}}}`

	reader := authntest.ConfigReader(t, Module(), body)
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	participants := []string{ModuleName}
	if withBus {
		reg.Register(core.Module{
			Name:     "stand-in.bus",
			Provides: []core.Provision{{Token: (*event.Bus)(nil), Exclusive: true}},
			New:      func(context.Context, *core.Registry) (any, error) { return bus, nil },
		})
		participants = append(participants, "stand-in.bus")
	}
	if store != nil {
		reg.Register(core.Module{
			Name:     "stand-in.settings",
			Provides: []core.Provision{{Token: (*settings.Store)(nil)}},
			New:      func(context.Context, *core.Registry) (any, error) { return store, nil },
		})
		participants = append(participants, "stand-in.settings")
	}
	if err := reg.Drive(context.Background(), core.Drive{Depth: core.StageInit, Participants: participants}); err != nil {
		t.Fatalf("driving the module: %v", err)
	}
}
