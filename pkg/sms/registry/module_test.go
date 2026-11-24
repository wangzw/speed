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
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/sms"
)

type stubDatabase struct {
	handle  *gorm.DB
	dialect db.Dialect
}

func (d stubDatabase) DB() *gorm.DB                                   { return d.handle }
func (d stubDatabase) Dialect() db.Dialect                            { return d.dialect }
func (d stubDatabase) BlindIndex(string) string                       { return "" }
func (d stubDatabase) Open(context.Context, string) (*gorm.DB, error) { return nil, nil }

type noConfig struct{}

func (noConfig) Decode(string, any) error { return nil }

type configWith struct{ templates map[string]sms.TemplateSpec }

func (c configWith) Decode(_ string, target any) error {
	if cfg, ok := target.(*moduleConfig); ok {
		cfg.Templates = c.templates
	}
	return nil
}

func providingDB(handle *gorm.DB) core.Module {
	return core.Module{
		Name:     "test.database",
		Provides: []core.Provision{{Token: (*db.Database)(nil), Exclusive: true}},
		New: func(context.Context, *core.Registry) (any, error) {
			return stubDatabase{handle: handle, dialect: db.SQLite}, nil
		},
	}
}

func providingProvider(p sms.Provider) core.Module {
	return core.Module{
		Name:     "test.provider",
		Provides: []core.Provision{{Token: (*sms.Provider)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return p, nil },
	}
}

func providingBus(bus event.Bus) core.Module {
	return core.Module{
		Name:     "test.event",
		Provides: []core.Provision{{Token: (*event.Bus)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return bus, nil },
	}
}

func openHandle(t *testing.T) *gorm.DB {
	t.Helper()
	handle := dbtest.SQLite(t, filepath.Join(t.TempDir(), "sms_registry.db"), nil).DB().
		Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, handle, sqliteDialect)
	return handle
}

// TestTheModuleDeliversAndManages drives the module through a full lifecycle: the delivered
// registry resolves, registers, lists and disables, and the module subscribes to the change event.
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

// TestTheDescriptorDeclaresTheLoggingCapability checks that the descriptor declares the
// logging capability Init resolves, which orders this module after the logging module.
func TestTheDescriptorDeclaresTheLoggingCapability(t *testing.T) {
	loggerToken := core.Token((*log.Logger)(nil))
	declared := false
	for _, r := range Module().Requires {
		if r.Token != loggerToken {
			continue
		}
		declared = true
		if !r.Optional {
			t.Errorf("the logging requirement is not optional; a host that assembles no logging " +
				"still has to build this module, so the declaration must not make the logging " +
				"module a hard dependency of the commands that name this one")
		}
	}
	if !declared {
		t.Errorf("Requires = %+v, want an optional (*log.Logger)(nil) requirement beside the "+
			"provider and the database", Module().Requires)
	}
}

// TestPrepareRefusesAnInconsistentConfiguration checks that a self-inconsistent template set stops the drive in Prepare.
func TestPrepareRefusesAnInconsistentConfiguration(t *testing.T) {
	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(configWith{templates: map[string]sms.TemplateSpec{
		"bad": {Params: []string{"code"}, Codes: map[string]string{}},
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
