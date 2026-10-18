package login_test

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/login"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	featureboard "github.com/vislake/speed/pkg/feature/board"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/routecontract"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
)

// The cases here drive the real feature board through a real registry, to Start,
// beside the real login engine. What a post made by hand cannot show is that it
// lands in the document at all: a post from New panics inside the board, and a
// conditional name the module did not declare fails the board's own seal.

// stubSMSCodes and stubEmailSender stand in for the optional seams the account
// self-service routes switch on. The engine only checks their presence; the
// routes these cases reach call none of their methods.
type (
	stubSMSCodes    struct{ authn.SMSCodes }
	stubEmailSender struct{ authn.EmailCodeSender }
)

// openGate stands in for the authentication gateway: the board registers its
// route as impersonable and asks nothing else of it.
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

// names returns the set the board froze in Start.
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
func (captureLogger) Redaction() log.Redaction    { return noRules{} }

// driveWithBoard assembles the login engine, the real board and the stand-ins
// both need, and advances to Start. It returns the mounter, so a case reads the
// routes the engine registered, and the frozen feature names.
func driveWithBoard(t *testing.T, sms, email bool) (*mounttest.Mounter, []string) {
	t.Helper()

	mounter := mounttest.New(t)
	router := routecontract.NewRouter("account")
	capture := &featureCapture{}
	reader := loadConfig(t, `{"feature": {"board": {"endpoint": "account"}}}`,
		featureboard.Module(), login.Module())

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(login.Module())
	reg.Register(featureboard.Module())
	participants := []string{login.ModuleName, featureboard.ModuleName}

	register := func(m core.Module) {
		reg.Register(m)
		participants = append(participants, m.Name)
	}
	register(standIn("stand-in.mounter", mounter, true, (*mount.Mounter)(nil)))
	register(standIn("stand-in.kv", &stubKV{}, true, (*kv.KV)(nil)))
	register(standIn("stand-in.accounts", &stubAccounts{}, true, (*authn.Accounts)(nil)))
	register(standIn("stand-in.admin", &stubAdmin{}, true, (*authn.AccountAdmin)(nil)))
	register(standIn("stand-in.memberships", &stubMemberships{}, true, (*authn.Memberships)(nil)))
	register(standIn("stand-in.passwords", &stubPasswords{}, true, (*authn.Passwords)(nil)))
	register(standIn("stand-in.throttle", &stubThrottle{}, true, (*authn.Throttle)(nil)))
	register(standIn("stand-in.sessions", &stubSessions{}, true, (*authn.Sessions)(nil)))
	register(standIn("stand-in.mfa", &stubMFA{}, true, (*authn.MFA)(nil)))
	register(methodModule(0, "password"))
	register(standIn("stand-in.router", router, true, (*speedhttp.Router)(nil)))
	register(standIn("stand-in.authn-gate", openGate{}, true, (*authn.Gate)(nil)))
	register(standIn("stand-in.logging", captureLogger{capture: capture}, true, (*log.Logger)(nil)))
	if sms {
		register(standIn("stand-in.sms-codes", &stubSMSCodes{}, true, (*authn.SMSCodes)(nil)))
	}
	if email {
		register(standIn("stand-in.email", &stubEmailSender{}, true, (*authn.EmailCodeSender)(nil)))
	}

	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageStart, Participants: participants}); err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	return mounter, capture.names(t)
}

// TestThePhoneSurfaceAndItsFeatureShareOneJudgement holds the phone half:
// SMSCodes present is exactly the post of authn.phone and exactly the two phone
// routes. An unconditional post fails the absent case; a route registered
// without the post fails the present one.
func TestThePhoneSurfaceAndItsFeatureShareOneJudgement(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sms   bool
		email bool
	}{
		{name: "no optional seam"},
		{name: "sms only", sms: true},
		{name: "email only", email: true},
		{name: "both", sms: true, email: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mounter, names := driveWithBoard(t, tc.sms, tc.email)

			if got := slices.Contains(names, "authn.phone"); got != tc.sms {
				t.Errorf("authn.phone present = %v, want %v; the set is %v", got, tc.sms, names)
			}
			if got := slices.Contains(names, "authn.identifiers.email"); got != tc.email {
				t.Errorf("authn.identifiers.email present = %v, want %v; the set is %v", got, tc.email, names)
			}
			_, phone := mounter.Registered(http.MethodPost, "/api/v1/auth/phone")
			_, verify := mounter.Registered(http.MethodPost, "/api/v1/auth/phone/verify")
			if phone != tc.sms || verify != tc.sms {
				t.Errorf("phone routes registered = (%v, %v), want (%v, %v)", phone, verify, tc.sms, tc.sms)
			}
		})
	}
}
