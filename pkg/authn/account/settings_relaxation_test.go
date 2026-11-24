package account

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

const (
	testSignupKey         = "authn.account.signup"
	testPasswordSignupKey = "authn.account.password-signup"
	testSignupIPLimitKey  = "authn.account.signup-ip-limit"
	testSignupHourlyKey   = "authn.account.signup-global-hourly"
)

// Moving either switch to its relaxing value publishes a change answered by one Warn naming the key and one best-effort relaxed audit record.
func TestRelaxingAnAccountSwitchWritesAWarnAndAnAuditRecord(t *testing.T) {
	for _, c := range []struct {
		key, relaxing, strict string
	}{
		{testSignupKey, "open", "closed"},
		{testPasswordSignupKey, "open", "closed"},
	} {
		t.Run(c.key, func(t *testing.T) {
			store, bus := deliveredSettings(t)
			h := newHarness(t, db.SQLite)
			h.p.store = store
			audit := h.withAudit()

			writeDeliveredSetting(t, store, c.key, c.relaxing)
			deliverChange(t, h, bus.lastChange(t))

			logs := h.logs.String()
			if n := strings.Count(logs, "level=WARN"); n != 1 {
				t.Fatalf("the log holds %d Warn lines, want 1:\n%s", n, logs)
			}
			if lines := h.logs.lines("level=WARN", "setting="+c.key); len(lines) != 1 {
				t.Fatalf("the Warn line does not name the setting %s:\n%s", c.key, logs)
			} else {
				if !strings.Contains(lines[0], "direction="+moveRelaxed) {
					t.Errorf("the Warn line does not carry direction %s:\n%s", moveRelaxed, lines[0])
				}
				if strings.Contains(lines[0], c.relaxing) {
					t.Errorf("the Warn line names the value %q:\n%s", c.relaxing, lines[0])
				}
			}
			entries := audit.written()
			if len(entries) != 1 {
				t.Fatalf("the audit holds %d rows, want 1", len(entries))
			}
			if entries[0].Action != relaxedAction || entries[0].Object != c.key || entries[0].Outcome != relaxedOutcome {
				t.Errorf("audit row = %+v, want the relaxed action on %s", entries[0], c.key)
			}
			if got := entries[0].Detail["change"]; got != moveRelaxed {
				t.Errorf("the audit detail change = %q, want %q", got, moveRelaxed)
			}

			writeDeliveredSetting(t, store, c.key, c.strict)
			deliverChange(t, h, bus.lastChange(t))

			if len(audit.written()) != 1 {
				t.Errorf("a change back to the strict value wrote %d audit rows, want the one from the relaxation", len(audit.written()))
			}
			if n := strings.Count(h.logs.String(), "level=WARN"); n != 1 {
				t.Errorf("a change back to the strict value left %d Warn lines, want the one from the relaxation", n)
			}
		})
	}
}

// Relaxing either registration budget writes a Warn and an audit record.
func TestRelaxingARegistrationBudgetWritesAWarnAndAnAuditRecord(t *testing.T) {
	for _, c := range []struct {
		name          string
		key           string
		before, after int
		direction     string
	}{
		{"the address budget turned off", testSignupIPLimitKey, 120, 0, moveToZero},
		{"the address budget raised", testSignupIPLimitKey, 120, 240, moveUp},
		{"the hourly budget raised", testSignupHourlyKey, 100, 200, moveUp},
	} {
		t.Run(c.name, func(t *testing.T) {
			store, bus := deliveredSettings(t)
			h := newHarness(t, db.SQLite)
			h.p.store = store
			audit := h.withAudit()

			writeDeliveredSetting(t, store, c.key, c.before)
			writeDeliveredSetting(t, store, c.key, c.after)
			deliverChange(t, h, bus.lastChange(t))

			logs := h.logs.String()
			lines := h.logs.lines("level=WARN", "setting="+c.key)
			if len(lines) != 1 {
				t.Fatalf("the log holds %d Warn line(s) for %s, want 1:\n%s", len(lines), c.key, logs)
			}
			if !strings.Contains(lines[0], "direction="+c.direction) {
				t.Errorf("the Warn line does not carry direction %s:\n%s", c.direction, lines[0])
			}
			entries := audit.written()
			if len(entries) != 1 {
				t.Fatalf("the audit holds %d rows, want 1", len(entries))
			}
			if entries[0].Object != c.key || entries[0].Detail["change"] != c.direction {
				t.Errorf("audit row = %+v, want the relaxed action on %s with change %s", entries[0], c.key, c.direction)
			}
		})
	}
}

// Tightening either budget, including the per-address one, writes nothing.
func TestTighteningARegistrationBudgetWritesNothing(t *testing.T) {
	for _, c := range []struct {
		name          string
		key           string
		before, after int
	}{
		{"the address budget lowered", testSignupIPLimitKey, 240, 120},
		{"the address budget switched on", testSignupIPLimitKey, 0, 120},
		{"the hourly budget lowered", testSignupHourlyKey, 100, 50},
	} {
		t.Run(c.name, func(t *testing.T) {
			store, bus := deliveredSettings(t)
			h := newHarness(t, db.SQLite)
			h.p.store = store
			audit := h.withAudit()

			writeDeliveredSetting(t, store, c.key, c.before)
			writeDeliveredSetting(t, store, c.key, c.after)
			deliverChange(t, h, bus.lastChange(t))

			if n := strings.Count(h.logs.String(), "level=WARN"); n != 0 {
				t.Errorf("a tightening write left %d Warn lines, want none:\n%s", n, h.logs.String())
			}
			if entries := audit.written(); len(entries) != 0 {
				t.Errorf("a tightening write wrote %d audit rows, want none: %+v", len(entries), entries)
			}
		})
	}
}

// A write at a tenant layer is not a relaxation, since both switches are read from the global layer alone.
func TestATenantLayerWriteIsNotARelaxation(t *testing.T) {
	store, bus := deliveredSettings(t)
	h := newHarness(t, db.SQLite)
	h.p.store = store
	audit := h.withAudit()

	write := settings.Write{Layer: settings.ForTenant("acme"), Key: testSignupKey, Value: "open", Actor: "test"}
	if err := store.Set(context.Background(), write); err != nil {
		t.Fatalf("writing %s at the tenant layer: %v", testSignupKey, err)
	}
	deliverChangeAt(t, h, "acme", bus.lastChange(t))

	if n := strings.Count(h.logs.String(), "level=WARN"); n != 0 {
		t.Errorf("a tenant-layer write left %d Warn lines, want none:\n%s", n, h.logs.String())
	}
	if entries := audit.written(); len(entries) != 0 {
		t.Errorf("a tenant-layer write wrote %d audit rows, want none: %+v", len(entries), entries)
	}
}

// A configuration file still carrying either removed switch is an unknown key and refuses startup, naming the key.
func TestAConfigurationCarryingARemovedAccountSwitchFailsStartup(t *testing.T) {
	for _, c := range []struct {
		key, value string
	}{
		{testSignupKey, "open"},
		{testPasswordSignupKey, "open"},
		{"authn.account.auto-link", "verified-email"},
	} {
		t.Run(c.key, func(t *testing.T) {
			name := strings.TrimPrefix(c.key, ConfigNamespace+".")
			body := `{"authn":{"account":{"` + name + `":"` + c.value + `"}}}`
			err := loadAccountConfiguration(t, body)
			if !errors.Is(err, config.ErrUnknownKey) {
				t.Fatalf("a configuration carrying %s loaded with %v, want config.ErrUnknownKey", c.key, err)
			}
			if !strings.Contains(err.Error(), c.key) {
				t.Errorf("the refusal %q does not name the key %s", err, c.key)
			}
		})
	}
}

// The settings store refuses a write to the retired auto-link key no module declares.
func TestTheRemovedAutoLinkSettingHasNoDeclaration(t *testing.T) {
	store, _ := deliveredSettings(t)
	write := settings.Write{
		Layer: settings.Global(), Key: "authn.account.auto-link", Value: "verified-email", Actor: "test",
	}
	if err := store.Set(context.Background(), write); !errors.Is(err, settings.ErrUnknownKey) {
		t.Fatalf("writing the removed switch returned %v, want settings.ErrUnknownKey", err)
	}
}

func deliverChange(t *testing.T, h *harness, change settings.Change) {
	t.Helper()
	deliverChangeAt(t, h, "", change)
}

func deliverChangeAt(t *testing.T, h *harness, tenant string, change settings.Change) {
	t.Helper()
	if err := h.p.onSettingsChanged(context.Background(), event.Event{
		Type:     settings.ChangedEventType,
		TenantID: tenant,
		Payload:  change,
	}); err != nil {
		t.Fatalf("the relaxation handler returned %v", err)
	}
}

func loadAccountConfiguration(t *testing.T, body string) error {
	t.Helper()
	locator := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(locator, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the primary config source: %v", err)
	}
	reg := core.New()
	reg.Register(jsonformat.Module())
	reg.Register(filesource.Module())
	reg.Register(core.Module{
		Name:      "host",
		Resources: []any{config.HostIdentity{Prefix: "SPEEDACCOUNTREMOVAL", DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	_, err := config.Load(t.Context(), reg, nil)
	return err
}

func writeDeliveredSetting(t *testing.T, store settings.Store, key string, value any) {
	t.Helper()
	write := settings.Write{Layer: settings.Global(), Key: key, Value: value, Actor: "test"}
	if err := store.Set(context.Background(), write); err != nil {
		t.Fatalf("writing %s: %v, want it declared as a setting", key, err)
	}
}

type deliveredBus struct {
	mu        sync.Mutex
	published []event.Event
}

func (b *deliveredBus) Publish(_ context.Context, evt event.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, evt)
	return nil
}

func (b *deliveredBus) Subscribe(string, string, event.Handler) {}

func (b *deliveredBus) lastChange(t *testing.T) settings.Change {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.published) - 1; i >= 0; i-- {
		if b.published[i].Type != settings.ChangedEventType {
			continue
		}
		var change settings.Change
		if err := event.DecodePayload(b.published[i].Payload, &change); err != nil {
			t.Fatalf("decoding the change payload: %v", err)
		}
		return change
	}
	t.Fatalf("the store published no %s event", settings.ChangedEventType)
	return settings.Change{}
}

func deliveredSettings(t *testing.T) (settings.Store, *deliveredBus) {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "settings.db"), nil)
	applySettingsMigrations(t, database.DB())
	bus := &deliveredBus{}

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(deliveredConfig{}, config.ModuleName)
	reg.Register(log.Module())
	reg.Supply(deliveredLogger{}, log.ModuleName)
	reg.Register(core.Module{
		Name:     "test.database",
		Provides: []core.Provision{{Token: (*db.Database)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return database, nil },
	})
	reg.Register(core.Module{
		Name:     "test.event",
		Provides: []core.Provision{{Token: (*event.Bus)(nil), Exclusive: true}},
		New:      func(context.Context, *core.Registry) (any, error) { return bus, nil },
	})
	reg.Register(settings.Module())
	reg.Register(Module())

	var store settings.Store
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{"test.database", "test.event", settings.ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			resolved, err := core.Resolve[settings.Store](reg)
			if err != nil {
				return err
			}
			store = resolved
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the settings module: %v", err)
	}
	return store, bus
}

func applySettingsMigrations(t *testing.T, handle *gorm.DB) {
	t.Helper()
	var tree fs.FS
	for _, resource := range settings.Module().Resources {
		if declared, ok := resource.(db.Migrations); ok {
			tree = declared.FS
		}
	}
	if tree == nil {
		t.Fatal("the settings module declares no migrations")
	}
	files, err := fs.Glob(tree, string(db.SQLite)+"/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("finding the settings module's SQLite migrations: %v (%d files)", err, len(files))
	}
	for _, name := range files {
		sql, err := fs.ReadFile(tree, name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for statement := range strings.SplitSeq(string(sql), ";") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if err := handle.Exec(statement).Error; err != nil {
				t.Fatalf("applying %s: %v", name, err)
			}
		}
	}
}

type deliveredConfig struct{}

func (deliveredConfig) Decode(string, any) error { return nil }

type deliveredLogger struct{}

func (deliveredLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (deliveredLogger) Redaction() log.Redaction  { return nil }
