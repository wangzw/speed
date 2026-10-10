package relay

import (
	"context"
	"slices"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
)

// hostOutbox is an outbox the host built itself, standing where the relay would
// be. It keeps the Stop and Close it is asked for, which is how a case sees
// whether the module's callbacks reached it.
type hostOutbox struct {
	mu    sync.Mutex
	calls []string
}

func (*hostOutbox) Stage(*gorm.DB, event.Event) error { return nil }

func (o *hostOutbox) Stop(context.Context) error  { o.record("stop"); return nil }
func (o *hostOutbox) Close(context.Context) error { o.record("close"); return nil }

func (o *hostOutbox) record(call string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, call)
}

func (o *hostOutbox) asked() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.calls)
}

// unusedDatabase is a database nothing in the drive opens: the one module that
// would take it up is the relay, whose New does not run for a supplied instance.
// Any call on it fails the case.
type unusedDatabase struct{ db.Database }

// placeholder is a module that declares one capability and nothing else, so an
// instance supplied for it stands in for the capability's own module, which the
// relay's requirements would otherwise pull in with its own requirements.
func placeholder(name string, capability core.Token) core.Module {
	return core.Module{Name: name, Provides: []core.Provision{{Token: capability}}}
}

// TestASuppliedOutboxIsLeftAloneByTheModuleCallbacks pins what the module does
// with an instance the host supplied for it: nothing. The start, stop and close
// callbacks act on a relay and on nothing else, so the supplied outbox is neither
// started nor asked to stop or close, and a callback that took it for a relay
// would panic at the start stage.
//
// The relay's two required capabilities are supplied for placeholder modules, so
// the drive reaches the start stage without the database and the bus a real
// assembly would build.
func TestASuppliedOutboxIsLeftAloneByTheModuleCallbacks(t *testing.T) {
	supplied := &hostOutbox{}
	reg := core.New()
	reg.Register(placeholder("case.database", (*db.Database)(nil)))
	reg.Register(placeholder("case.bus", (*event.Bus)(nil)))
	reg.Register(Module())
	reg.Supply(unusedDatabase{}, "case.database")
	reg.Supply(newRecordingBus("case", &ledger{}), "case.bus")
	reg.Supply(supplied, ModuleName)

	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageStart}); err != nil {
		t.Fatalf("a drive to the start stage over an outbox the host supplied for %s failed: %v", ModuleName, err)
	}
	if got := supplied.asked(); len(got) != 0 {
		t.Fatalf("the module's callbacks asked the supplied outbox for %v: an instance that is not the "+
			"relay is not theirs to stop or close, and what it holds is released by the host", got)
	}
}
