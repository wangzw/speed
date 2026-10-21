package token

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/gate"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/internal/verdict"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	eventmemory "github.com/vislake/speed/pkg/event/memory"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// testCheckSessionKey is the setting this module carries for the session step.
// It is written out here rather than read from a constant so that a case names
// the key the design lists.
const testCheckSessionKey = "authn.token.check-session"

// TestTheCheckSessionSettingOffLetsARevokedSessionVerify holds that the session
// step is governed at run time by the settings store: a token naming a session
// the store no longer holds is refused under the declared default and verifies
// once authn.token.check-session is written false.
func TestTheCheckSessionSettingOffLetsARevokedSessionVerify(t *testing.T) {
	store, _ := deliveredSettings(t)
	v := newVfy(t)
	v.sess.notFound = true
	v.p.settings.store = store

	requireDenied(t, v.verify(v.good()))

	writeDeliveredSetting(t, store, testCheckSessionKey, false)

	if got := outcomeOf(v.verify(v.good())); got != verdict.Authenticated {
		t.Fatalf("a token naming a revoked session reads as %s with authn.token.check-session false, want authenticated", got)
	}
}

// TestRelaxingTheCheckSessionSettingWritesAWarnAndAnAuditRecord holds the
// visibility of turning the session check off: the write publishes a change, the
// module's subscription answers it with one Warn naming the key and never the
// value, and one best-effort audit record with the relaxed action. A change back
// to the strict value writes nothing.
func TestRelaxingTheCheckSessionSettingWritesAWarnAndAnAuditRecord(t *testing.T) {
	store, bus := deliveredSettings(t)
	r := newRig(t)
	r.p.settings.store = store

	writeDeliveredSetting(t, store, testCheckSessionKey, false)
	feedChange(r, bus.lastChange(t))

	logs := r.logs.String()
	if n := strings.Count(logs, "level=WARN"); n != 1 {
		t.Fatalf("the log holds %d Warn lines, want 1:\n%s", n, logs)
	}
	if !strings.Contains(logs, "setting="+testCheckSessionKey) {
		t.Errorf("the Warn line does not name the setting %s:\n%s", testCheckSessionKey, logs)
	}
	if !strings.Contains(logs, "direction="+dirRelaxed) {
		t.Errorf("the Warn line does not carry the direction %s:\n%s", dirRelaxed, logs)
	}
	if strings.Contains(logs, "=false") || strings.Contains(logs, "=true") {
		t.Errorf("the Warn line names the value:\n%s", logs)
	}
	if len(r.audit.entries) != 1 {
		t.Fatalf("the audit holds %d rows, want 1", len(r.audit.entries))
	}
	entry := r.audit.entries[0]
	if entry.Action != relaxedAction || entry.Object != testCheckSessionKey || entry.Outcome != relaxedOutcome {
		t.Errorf("audit row = %+v, want the relaxed action on %s", entry, testCheckSessionKey)
	}
	if got := entry.Detail["change"]; got != dirRelaxed {
		t.Errorf("the audit detail change = %q, want %q", got, dirRelaxed)
	}

	writeDeliveredSetting(t, store, testCheckSessionKey, true)
	feedChange(r, bus.lastChange(t))

	if len(r.audit.entries) != 1 {
		t.Errorf("a change back to the strict value wrote %d audit rows, want the one from the relaxation", len(r.audit.entries))
	}
	if n := strings.Count(r.logs.String(), "level=WARN"); n != 1 {
		t.Errorf("a change back to the strict value left %d Warn lines, want the one from the relaxation", n)
	}
}

// TestATenantLayerWriteIsNotARelaxation holds that a write at a tenant layer is
// not reported: the switch is read from the global layer alone, so a tenant row
// leaves the effective value unchanged and must not manufacture a relaxation
// warning or an audit row.
func TestATenantLayerWriteIsNotARelaxation(t *testing.T) {
	store, bus := deliveredSettings(t)
	r := newRig(t)
	r.p.settings.store = store

	write := settings.Write{Layer: settings.ForTenant("acme"), Key: testCheckSessionKey, Value: false, Actor: "test"}
	if err := store.Set(t.Context(), write); err != nil {
		t.Fatalf("writing %s at the tenant layer: %v", testCheckSessionKey, err)
	}
	feedChangeAt(r, "acme", bus.lastChange(t))

	if n := strings.Count(r.logs.String(), "level=WARN"); n != 0 {
		t.Errorf("a tenant-layer write left %d Warn lines, want none:\n%s", n, r.logs.String())
	}
	if len(r.audit.entries) != 0 {
		t.Errorf("a tenant-layer write wrote %d audit rows, want none: %+v", len(r.audit.entries), r.audit.entries)
	}
}

// TestAConfigurationCarryingTheRemovedCheckSessionItemFailsStartup holds the
// move out of configuration: a configuration file that still carries the item is
// an unknown key and refuses the startup, naming the key.
func TestAConfigurationCarryingTheRemovedCheckSessionItemFailsStartup(t *testing.T) {
	_, err := loadConfig(t, `{"authn":{"token":{"issuer":"https://auth.example.com","check-session":false}}}`)
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Fatalf("a configuration carrying authn.token.check-session loaded with %v, want config.ErrUnknownKey", err)
	}
	if !strings.Contains(err.Error(), testCheckSessionKey) {
		t.Errorf("the refusal %q does not name the key %s", err, testCheckSessionKey)
	}
}

// writeDeliveredSetting writes one value into a store the settings module
// delivered. A failure is fatal: the case exists to hold the key is declared.
func writeDeliveredSetting(t *testing.T, store settings.Store, key string, value any) {
	t.Helper()
	write := settings.Write{Layer: settings.Global(), Key: key, Value: value, Actor: "test"}
	if err := store.Set(context.Background(), write); err != nil {
		t.Fatalf("writing %s: %v, want it declared as a setting", key, err)
	}
}

// deliveredBus is the event bus of the settings assembly: it keeps what the
// store publishes, so a case can hand the change to the module's subscription
// with the payload the store itself built.
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

// lastChange decodes the payload of the most recent settings.changed event.
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

// deliveredSettings runs this module's declaration through the settings module
// that builds the key table from it, over a SQLite database the database module
// opened, and returns the store it delivers and the bus it publishes on. This
// module is registered for its declaration alone, the way the key table reads
// every registered module's: it does not take part in the drive.
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

// applySettingsMigrations runs the settings module's own SQLite migrations, read
// out of the declaration it registers.
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

// deliveredConfig is the configuration reader of an assembly that configures
// nothing, which leaves every declared default in place.
type deliveredConfig struct{}

func (deliveredConfig) Decode(string, any) error { return nil }

// deliveredLogger is the logging capability of an assembly whose records nobody
// reads.
type deliveredLogger struct{}

func (deliveredLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (deliveredLogger) Redaction() log.Redaction  { return nil }

// capturedLogger is a logging capability whose records a case reads back.
type capturedLogger struct{ logger *slog.Logger }

func (c capturedLogger) Named(string) *slog.Logger { return c.logger }
func (c capturedLogger) Redaction() log.Redaction  { return nil }

// TestTheCheckSessionSwitchIsReadAfterTheSettingsSnapshotLoads drives the real
// registry through both modules' Init in one assembly. The settings snapshot is
// loaded in its Init, so the switch set to false before the process starts must
// be visible to the token module's startup diagnostic: one Warn naming the key
// and direction relax, and no read-failure Error. It fails when the token module
// leaves the settings store out of its Requires, because the dependency order
// then places its Init first.
func TestTheCheckSessionSwitchIsReadAfterTheSettingsSnapshotLoads(t *testing.T) {
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "assembly.db"), nil)
	applySettingsMigrations(t, database.DB())
	if err := database.DB().Exec(
		`INSERT INTO settings (tenant_id, key, value, version, updated_at) VALUES ('', ?, 'false', 1, ?)`,
		testCheckSessionKey, time.Now(),
	).Error; err != nil {
		t.Fatalf("writing %s into the settings table: %v", testCheckSessionKey, err)
	}

	mounter := mounttest.New(t)
	keyPath := generatedKey(t, algES256)
	body := `{"authn":{"token":{"issuer":"https://auth.example.com","algorithm":"ES256","signing-key-file":"` +
		keyPath + `"}}}`
	reader := authntest.ConfigReaderAll(t, body, Module(), gate.Module())
	logs := &logBuffer{}

	// The providers are named so that all of them sort before "settings", as the
	// real ones do ("authn.account", "authn.mount", "authn.session"): without the
	// token module's optional requirement on the settings store, the graph then
	// leaves both modules ready at once and takes "authn.token" first.
	const (
		providerDB        = "provider.db"
		providerMount     = "provider.mount"
		providerAccounts  = "provider.accounts"
		providerSessions  = "provider.sessions"
		providerPasswords = "provider.passwords"
		providerThrottle  = "provider.throttle"
	)
	reg := core.New()
	reg.Register(Module())
	reg.Register(standIn(providerDB, (*db.Database)(nil)))
	reg.Register(standIn(providerMount, (*mount.Mounter)(nil)))
	reg.Register(standIn(providerAccounts, (*authn.Accounts)(nil)))
	reg.Register(standIn(providerSessions, (*authn.Sessions)(nil)))
	reg.Register(standIn(providerPasswords, (*authn.Passwords)(nil)))
	reg.Register(standIn(providerThrottle, (*authn.Throttle)(nil)))
	reg.Register(config.Module())
	reg.Register(log.Module())
	reg.Register(settings.Module())
	reg.Register(eventmemory.Module())
	reg.Supply(database, providerDB)
	reg.Supply(mounter, providerMount)
	reg.Supply(&fakeAccounts{}, providerAccounts)
	reg.Supply(newFakeSessions(), providerSessions)
	reg.Supply(newFakePasswords(), providerPasswords)
	reg.Supply(newFakeThrottle(), providerThrottle)
	reg.Supply(reader, config.ModuleName)
	reg.Supply(capturedLogger{logger: logs.logger()}, log.ModuleName)

	err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageInit,
		Participants: []string{
			ModuleName, providerDB, providerMount, providerAccounts, providerSessions,
			providerPasswords, providerThrottle, config.ModuleName, log.ModuleName,
			settings.ModuleName, eventmemory.ModuleName,
		},
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}

	out := logs.String()
	if strings.Contains(out, "cannot be read") {
		t.Errorf("the switch was read before the snapshot was loaded:\n%s", out)
	}
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "setting="+testCheckSessionKey) ||
		!strings.Contains(out, "direction="+dirRelaxed) {
		t.Errorf("the startup diagnostic does not name the relaxed switch and its direction:\n%s", out)
	}
}
