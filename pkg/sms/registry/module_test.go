package registry

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/sms"
)

// stubDatabase is the database capability over a real handle. The module calls
// only DB, so the rest of the interface is there to satisfy it.
type stubDatabase struct {
	handle  *gorm.DB
	dialect db.Dialect
}

func (d stubDatabase) DB() *gorm.DB                                   { return d.handle }
func (d stubDatabase) Dialect() db.Dialect                            { return d.dialect }
func (d stubDatabase) BlindIndex(string) string                       { return "" }
func (d stubDatabase) Open(context.Context, string) (*gorm.DB, error) { return nil, nil }

// noConfig is the reader a host supplies when nothing is configured. It decodes
// nothing, which leaves the module's zero configuration in place — no templates
// and no poll interval.
type noConfig struct{}

func (noConfig) Decode(string, any) error { return nil }

// configWith is a reader that hands the module a fixed configuration template
// set.
type configWith struct{ templates map[string]sms.TemplateSpec }

func (c configWith) Decode(_ string, target any) error {
	if cfg, ok := target.(*moduleConfig); ok {
		cfg.Templates = c.templates
	}
	return nil
}

// providingDB delivers the database capability.
func providingDB(handle *gorm.DB) core.Module {
	return core.Module{
		Name:     "test.database",
		Provides: []core.Provision{{Token: (*db.Database)(nil), Exclusive: true}},
		New: func(context.Context, *core.Registry) (any, error) {
			return stubDatabase{handle: handle, dialect: db.SQLite}, nil
		},
	}
}

// providingProvider delivers the template-management seam.
func providingProvider(p sms.Provider) core.Module {
	return core.Module{
		Name:     "test.provider",
		Provides: []core.Provision{{Token: (*sms.Provider)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return p, nil },
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

// openHandle delivers a quiet handle with the module's table built.
func openHandle(t *testing.T) *gorm.DB {
	t.Helper()
	handle := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms_registry.db"), nil).DB().
		Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, handle, sqliteDialect)
	return handle
}

// TestTheModuleDeliversAndManages drives the module through a full lifecycle and
// exercises the delivered capability: the registry resolves, registers, lists
// and disables, and the module subscribed to the change event.
func TestTheModuleDeliversAndManages(t *testing.T) {
	handle := openHandle(t)
	bus := newBus()
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(noConfig{}, config.ModuleName)
	reg.Register(providingDB(handle))
	reg.Register(providingProvider(newProvider()))
	reg.Register(providingBus(bus))
	reg.Register(Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageStart,
		Participants: []string{"test.database", "test.provider", "test.event", ModuleName},
		Action: func(ctx context.Context, reg *core.Registry) error {
			registry, err := core.Resolve[sms.Registry](reg)
			if err != nil {
				t.Fatalf("resolving the registry capability: %v", err)
			}
			if got := registry.Declared(); len(got) != 0 {
				t.Errorf("Declared returned %+v, want none", got)
			}
			rec, err := registry.Register(ctx, "otp", spec([]string{"code"}, map[string]string{"en": "c"}))
			if err != nil {
				t.Fatalf("registering through the delivered capability: %v", err)
			}
			if rec.Name != "otp" || rec.Approvals["en"].State != sms.ApprovalPending {
				t.Errorf("the registered record is %+v", rec)
			}
			records, err := registry.List(ctx)
			if err != nil {
				t.Fatalf("listing: %v", err)
			}
			if len(records) != 1 || records[0].Name != "otp" {
				t.Errorf("List returned %+v, want one otp record", records)
			}
			if err := registry.SetEnabled(ctx, "otp", false); err != nil {
				t.Errorf("disabling through the delivered capability: %v", err)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the lifecycle: %v", err)
	}
	if bus.subscribed(ChangedEventType) == 0 {
		t.Errorf("the module did not subscribe to %q", ChangedEventType)
	}
}

// TestPrepareRefusesAnInconsistentConfiguration covers the startup check: a
// configuration whose template set is not self-consistent stops the drive in
// Prepare.
func TestPrepareRefusesAnInconsistentConfiguration(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(configWith{templates: map[string]sms.TemplateSpec{
		// No parameter: a template that substitutes nothing is not one this
		// capability can resolve.
		"bad": {Codes: map[string]string{"en": "c"}},
	}}, config.ModuleName)
	reg.Register(providingDB(openHandle(t)))
	reg.Register(providingProvider(newProvider()))
	reg.Register(Module())

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StagePrepare,
		Participants: []string{"test.database", "test.provider", ModuleName},
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
	if err == nil {
		t.Fatal("driving with an inconsistent configuration returned nil")
	}
}
