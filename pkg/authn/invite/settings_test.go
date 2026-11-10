package invite

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
	jsonformat "github.com/vislake/speed/pkg/config/format/json"
	filesource "github.com/vislake/speed/pkg/config/source/file"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// testTTLKey is the setting this module carries for the configured lifetime. It
// is written out here rather than read from a constant so that the case names
// the key the design lists. testDefaultTTL is the declared default the design
// fixes.
const (
	testTTLKey     = "authn.invite.ttl"
	testDefaultTTL = 168 * time.Hour
)

// TestAStoredTTLShapesTheNextIssuance holds the runtime read: the lifetime is a
// setting read when an invitation is issued, so a value written after the module
// started shapes the invitation issued next. The declared default is 168h, and
// the value written here is distinctly shorter.
func TestAStoredTTLShapesTheNextIssuance(t *testing.T) {
	h := newSettingsAssembly(t)
	h.store.set(testTTLKey, 2*time.Hour)

	issued, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "runtime@example.com"})
	if err != nil {
		t.Fatalf("issuing an invitation: %v", err)
	}
	if want := h.clock.read().Add(2 * time.Hour); !issued.Invitation.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want the stored lifetime's %v", issued.Invitation.ExpiresAt, want)
	}
}

// TestATTLThatCannotBeReadReadsAsTheDefaultAndSaysSo holds the failure
// direction: a store that fails is the declared default, and the fault is
// reported at Error once, naming the setting.
func TestATTLThatCannotBeReadReadsAsTheDefaultAndSaysSo(t *testing.T) {
	h := newSettingsAssembly(t)
	h.store.failOn(testTTLKey, errors.New("the store is down"))

	issued, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "fallback@example.com"})
	if err != nil {
		t.Fatalf("issuing an invitation: %v", err)
	}
	if want := h.clock.read().Add(testDefaultTTL); !issued.Invitation.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want the declared default %v", issued.Invitation.ExpiresAt, want)
	}
	if lines := h.logs.lines("level=ERROR", "setting="+testTTLKey); len(lines) != 1 {
		t.Errorf("the log holds %d Error lines naming %s, want one:\n%s", len(lines), testTTLKey, h.logs.String())
	}
}

// TestAnOutOfDomainTTLReadsAsTheDefaultAndSaysSo holds the second failure
// direction: a value outside the declared domain is not acted on, the default
// is used, and the fault is reported at Error once.
func TestAnOutOfDomainTTLReadsAsTheDefaultAndSaysSo(t *testing.T) {
	h := newSettingsAssembly(t)
	h.store.set(testTTLKey, 9000*time.Hour)

	issued, err := h.p.Issue(context.Background(), IssueInvite{Tenant: tenant, Email: "domain@example.com"})
	if err != nil {
		t.Fatalf("issuing an invitation: %v", err)
	}
	if want := h.clock.read().Add(testDefaultTTL); !issued.Invitation.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %v, want the declared default %v", issued.Invitation.ExpiresAt, want)
	}
	if lines := h.logs.lines("level=ERROR", "setting="+testTTLKey); len(lines) != 1 {
		t.Errorf("the log holds %d Error lines naming %s, want one:\n%s", len(lines), testTTLKey, h.logs.String())
	}
}

// TestAConfigurationCarryingTheRemovedTTLFailsStartup holds the move out of
// configuration: a configuration file that still carries the lifetime is an
// unknown key and refuses the startup, naming the key.
func TestAConfigurationCarryingTheRemovedTTLFailsStartup(t *testing.T) {
	err := loadInviteConfiguration(t, `{"authn":{"invite":{"ttl":"2h"}}}`)
	if !errors.Is(err, config.ErrUnknownKey) {
		t.Fatalf("a configuration carrying %s loaded with %v, want config.ErrUnknownKey", testTTLKey, err)
	}
	if !strings.Contains(err.Error(), testTTLKey) {
		t.Errorf("the refusal %q does not name the key %s", err, testTTLKey)
	}
}

// settingsAssembly is the module's product over a migrated SQLite database,
// assembled through its descriptor so that the settings store it takes up is the
// one a host would have supplied.
type settingsAssembly struct {
	t     *testing.T
	p     *product
	clock *testClock
	store *fakeSettingsStore
	bus   *recordingEventBus
	audit *recordingAudit
	logs  *logBuffer
}

func newSettingsAssembly(t *testing.T) *settingsAssembly {
	t.Helper()
	database := dbtest.SQLite(t, filepath.Join(t.TempDir(), "invite-settings.db"), nil)
	applyMigrations(t, quietly(database.DB()))

	h := &settingsAssembly{
		t:     t,
		clock: newTestClock(),
		store: &fakeSettingsStore{values: map[string]any{}, errs: map[string]error{}},
		bus:   &recordingEventBus{handlers: map[string][]event.Handler{}},
		audit: &recordingAudit{},
		logs:  &logBuffer{},
	}

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(authntest.ConfigReader(t, Module(), "{}"), config.ModuleName)
	reg.Register(log.Module())
	reg.Supply(&capturedLogger{logger: h.logs.logger()}, log.ModuleName)
	supplyModule(t, reg, "test.database", (*db.Database)(nil), database)
	supplyModule(t, reg, "test.mounter", (*mount.Mounter)(nil), mounttest.New(t))
	supplyModule(t, reg, "test.kv", (*kv.KV)(nil), newFakeKV())
	supplyModule(t, reg, "test.accounts", (*authn.Accounts)(nil), newAccounts())
	supplyModule(t, reg, "test.account-admin", (*authn.AccountAdmin)(nil), &fakeAccountAdmin{})
	supplyModule(t, reg, "test.signup-state", (*authn.SignupVerification)(nil), &fakeSignupVerification{})
	supplyModule(t, reg, "test.memberships", (*authn.Memberships)(nil), &fakeMemberships{})
	supplyModule(t, reg, "test.audit", (*audit.Audit)(nil), h.audit)
	supplyModule(t, reg, "test.settings", (*settings.Store)(nil), h.store)
	supplyModule(t, reg, "test.event", (*event.Bus)(nil), h.bus)
	reg.Register(Module())

	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{ModuleName},
		Action: func(_ context.Context, reg *core.Registry) error {
			delivered, rerr := core.Resolve[Invites](reg)
			if rerr != nil {
				return rerr
			}
			p, ok := delivered.(*product)
			if !ok {
				t.Fatalf("the module delivered %T, want its own product", delivered)
			}
			p.now = h.clock.read
			h.p = p
			return nil
		},
	})
	if err != nil {
		t.Fatalf("assembling the module: %v", err)
	}
	return h
}

// supplyModule registers a module that delivers one capability and hands it the
// instance a case can read.
func supplyModule(t *testing.T, reg *core.Registry, name string, token core.Token, instance any) {
	t.Helper()
	reg.Register(core.Module{
		Name:     name,
		Provides: []core.Provision{{Token: token, Exclusive: true}},
	})
	reg.Supply(instance, name)
}

// loadInviteConfiguration runs the real loader over a JSON primary source,
// returning what loading it answered. Unlike the authntest reader it does not
// stop the case on a refusal: the refusal is what the case asserts.
func loadInviteConfiguration(t *testing.T, body string) error {
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
		Resources: []any{config.HostIdentity{Prefix: "SPEEDINVITEREMOVAL", DefaultLocator: "file://" + locator}},
	})
	reg.Register(Module())
	_, err := config.Load(t.Context(), reg, nil)
	return err
}

// lines are the log lines that carry every one of the fragments.
func (b *logBuffer) lines(fragments ...string) []string {
	var out []string
	for _, line := range strings.Split(b.String(), "\n") {
		matches := line != ""
		for _, f := range fragments {
			matches = matches && strings.Contains(line, f)
		}
		if matches {
			out = append(out, line)
		}
	}
	return out
}

// fakeSettingsStore is a settings store a case writes values into and reads
// through the same typed interface the module reads.
type fakeSettingsStore struct {
	mu     sync.Mutex
	values map[string]any
	errs   map[string]error
}

func (s *fakeSettingsStore) set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
}

func (s *fakeSettingsStore) failOn(key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs[key] = err
}

func (s *fakeSettingsStore) Get(_ settings.Layer, key string, target any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.errs[key]; err != nil {
		return err
	}
	value, ok := s.values[key]
	if !ok {
		return settings.ErrUnknownKey
	}
	reflect.ValueOf(target).Elem().Set(reflect.ValueOf(value))
	return nil
}

func (s *fakeSettingsStore) Set(context.Context, settings.Write) error { return nil }

func (s *fakeSettingsStore) Delete(context.Context, settings.Layer, string, string) error {
	return nil
}

func (s *fakeSettingsStore) List(context.Context, settings.Layer) ([]settings.Entry, error) {
	return nil, nil
}

func (s *fakeSettingsStore) Declared() []settings.DeclaredItem { return nil }

func (s *fakeSettingsStore) WrittenAt(settings.Layer, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (s *fakeSettingsStore) Read(key string, reads ...settings.LayerRead) ([]settings.LayerReadResult, error) {
	results := make([]settings.LayerReadResult, len(reads))
	for i, read := range reads {
		if err := s.Get(read.Layer, key, read.Target); err != nil {
			return nil, err
		}
		results[i] = settings.LayerReadResult{}
	}
	return results, nil
}

// recordingEventBus is the event bus of the assembly: a publish reaches every
// handler subscribed to the type, in the publisher's goroutine.
type recordingEventBus struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
}

func (b *recordingEventBus) Subscribe(eventType string, _ string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

func (b *recordingEventBus) Publish(ctx context.Context, evt event.Event) error {
	b.mu.Lock()
	handlers := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	for _, h := range handlers {
		if err := h(ctx, evt); err != nil {
			return err
		}
	}
	return nil
}

// deliver hands one event to every subscriber of its type.
func (b *recordingEventBus) deliver(t *testing.T, evt event.Event) {
	t.Helper()
	if err := b.Publish(context.Background(), evt); err != nil {
		t.Fatalf("delivering the event: %v", err)
	}
}

// capturedLogger is the logging capability of the assembly, writing into the
// case's buffer.
type capturedLogger struct {
	logger *slog.Logger
}

func (l *capturedLogger) Named(string) *slog.Logger { return l.logger }
func (l *capturedLogger) Redaction() log.Redaction  { return nil }
