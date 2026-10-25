package delivery

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/feature"
	featureboard "github.com/vislake/speed/pkg/feature/board"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/internal/configtest"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/jobs"
	kvmemory "github.com/vislake/speed/pkg/kv/memory"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/tenant"
)

// The cases here drive the real feature board through a real registry, to Start,
// beside the real delivery pipeline. Two things are held: a channel is named
// exactly when its seams are present — sms needs both of its seams, so a case
// with only the registry must not see the name — and a host-supplied instance
// posts nothing.

// featureQueue, featureHandlers, featureDatabase and the seam structs stand in
// for the required and optional capabilities. The pipeline only takes them up in
// New and registers its handler in Init; the seams are read for presence alone.
type (
	featureQueue       struct{ jobs.Queue }
	featureDatabase    struct{ db.Database }
	featureRoster      struct{ tenant.Catalog }
	featurePrefs       struct{ notification.Preferences }
	featureInbox       struct{ notification.Inbox }
	featureLocalizer   struct{ i18n.Localizer }
	featureMailer      struct{ mail.Sender }
	featureSMSRegistry struct{ sms.Registry }
	featureSMSSender   struct{ sms.Sender }
)

func (featureDatabase) DB() *gorm.DB { return nil }

type featureHandlers struct{}

func (featureHandlers) Register(jobs.Handler) error { return nil }
func (featureHandlers) Registered() []string        { return nil }

// openGate stands in for the authentication gateway, which only has to accept
// the board's impersonable registration.
type openGate struct{}

func (openGate) Exempt(string, string, string)       {}
func (openGate) Optional(string, string, string)     {}
func (openGate) Impersonable(string, string, string) {}

// featureCapture keeps what the board logs, so a case reads the frozen set back.
type featureCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (*featureCapture) Enabled(context.Context, slog.Level) bool { return true }

func (c *featureCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}

func (c *featureCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *featureCapture) WithGroup(string) slog.Handler      { return c }

func (c *featureCapture) names(t *testing.T) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Message != "feature set frozen" {
			continue
		}
		var names []string
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "names" {
				names, _ = a.Value.Any().([]string)
				return false
			}
			return true
		})
		return names
	}
	t.Fatal("the feature board logged no frozen set")
	return nil
}

type captureLogger struct{ capture *featureCapture }

func (l captureLogger) Named(string) *slog.Logger { return slog.New(l.capture) }
func (captureLogger) Redaction() log.Redaction    { return noRedaction{} }

type noRedaction struct{}

func (noRedaction) AddKeys(...string)              {}
func (noRedaction) AddPattern(string, log.Matcher) {}

// seams says which channel seams a case delivers.
type seams struct {
	mail     bool
	registry bool
	sender   bool
}

// driveWithBoard assembles the delivery module, the real board and the stand-ins
// both need, and advances to Start. A supplied instance for the delivery module
// replaces its own product, which is the host-supplied case.
func driveWithBoard(t *testing.T, on seams, supplied bool) []string {
	t.Helper()

	capture := &featureCapture{}
	router := routecontract.NewRouter("account")
	reader := configtest.Reader(t, `{"feature": {"board": {"endpoint": "account"}}}`,
		featureboard.Module(), kvmemory.Module(), Module())

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	reg.Register(featureboard.Module())
	participants := []string{ModuleName, featureboard.ModuleName}

	register := func(m core.Module) {
		reg.Register(m)
		participants = append(participants, m.Name)
	}
	register(kvmemory.Module())
	register(standIn("stand-in.queue", featureQueue{}, (*jobs.Queue)(nil)))
	register(standIn("stand-in.handlers", featureHandlers{}, (*jobs.Handlers)(nil)))
	register(standIn("stand-in.database", featureDatabase{}, (*db.Database)(nil)))
	register(standIn("stand-in.roster", featureRoster{}, (*tenant.Catalog)(nil)))
	register(standIn("stand-in.prefs", featurePrefs{}, (*notification.Preferences)(nil)))
	register(standIn("stand-in.inbox", featureInbox{}, (*notification.Inbox)(nil)))
	register(standIn("stand-in.localizer", featureLocalizer{}, (*i18n.Localizer)(nil)))
	register(standIn("stand-in.accounts", newStubAccounts(), (*authn.Accounts)(nil)))
	register(standIn("stand-in.router", router, (*speedhttp.Router)(nil)))
	register(standIn("stand-in.authn-gate", openGate{}, (*authn.Gate)(nil)))
	register(standIn("stand-in.logging", captureLogger{capture: capture}, (*log.Logger)(nil)))
	if on.mail {
		register(standIn("stand-in.mail", featureMailer{}, (*mail.Sender)(nil)))
	}
	if on.registry {
		register(standIn("stand-in.sms-registry", featureSMSRegistry{}, (*sms.Registry)(nil)))
	}
	if on.sender {
		register(standIn("stand-in.sms-sender", featureSMSSender{}, (*sms.Sender)(nil)))
	}

	if supplied {
		// The host supplies the module's own instance. It delivers the declared
		// seam, so the registry accepts it; the callbacks leave it alone.
		reg.Supply(struct{ notification.Dispatcher }{}, ModuleName)
	}

	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageStart, Participants: participants}); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	return capture.names(t)
}

// TestAChannelIsNamedExactlyWhenItsSeamsArePresent holds the email and sms
// judgements. The registry-only case is the one a one-seam implementation
// fails: sms is named only when both the registry and the sender are present.
func TestAChannelIsNamedExactlyWhenItsSeamsArePresent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		on       seams
		supplied bool
		email    bool
		sms      bool
	}{
		{name: "no seam"},
		{name: "mail", on: seams{mail: true}, email: true},
		{name: "registry only", on: seams{registry: true}, sms: false},
		{name: "sender only", on: seams{sender: true}, sms: false},
		{name: "registry and sender", on: seams{registry: true, sender: true}, sms: true},
		{name: "both channels", on: seams{mail: true, registry: true, sender: true}, email: true, sms: true},
		{name: "host-supplied instance", on: seams{mail: true, registry: true, sender: true}, supplied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names := driveWithBoard(t, tc.on, tc.supplied)
			if got := slices.Contains(names, featureEmail); got != tc.email {
				t.Errorf("%s present = %v, want %v; the set is %v", featureEmail, got, tc.email, names)
			}
			if got := slices.Contains(names, featureSMS); got != tc.sms {
				t.Errorf("%s present = %v, want %v; the set is %v", featureSMS, got, tc.sms, names)
			}
		})
	}
}

// TestTheModuleDeclaresBothChannelFeatures holds the declarations the posts are
// judged against: both names, both conditional.
func TestTheModuleDeclaresBothChannelFeatures(t *testing.T) {
	want := map[string]bool{featureEmail: true, featureSMS: true}
	got := map[string]bool{}
	for _, r := range Module().Resources {
		if s, ok := r.(feature.Spec); ok {
			if _, twice := got[s.Name]; twice {
				t.Errorf("%s is declared twice", s.Name)
			}
			got[s.Name] = s.Conditional
		}
	}
	for name, conditional := range want {
		declared, ok := got[name]
		if !ok {
			t.Errorf("%s is not declared", name)
			continue
		}
		if declared != conditional {
			t.Errorf("%s Conditional = %v, want %v", name, declared, conditional)
		}
	}
	if len(got) != len(want) {
		t.Errorf("declared %d names, want %d: %v", len(got), len(want), got)
	}
}
