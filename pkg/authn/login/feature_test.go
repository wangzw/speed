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

type (
	stubSMSCodes    struct{ authn.SMSCodes }
	stubEmailSender struct{ authn.EmailCodeSender }
)

type openGate struct{}

func (openGate) Exempt(string, string, string)       {}
func (openGate) Optional(string, string, string)     {}
func (openGate) Impersonable(string, string, string) {}

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
func (captureLogger) Redaction() log.Redaction    { return noRules{} }

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
	register(standIn("stand-in.signups", &stubSignups{}, true, (*authn.Signups)(nil)))
	register(standIn("stand-in.signup-state", &stubSignupState{}, true, (*authn.SignupVerification)(nil)))
	register(standIn("stand-in.memberships", &stubMemberships{}, true, (*authn.Memberships)(nil)))
	register(standIn("stand-in.passwords", &stubPasswords{}, true, (*authn.Passwords)(nil)))
	register(standIn("stand-in.throttle", &stubThrottle{}, true, (*authn.Throttle)(nil)))
	register(standIn("stand-in.sessions", &stubSessions{}, true, (*authn.Sessions)(nil)))
	register(standIn("stand-in.mfa", &stubMFA{}, true, (*authn.MFA)(nil)))
	register(standIn("stand-in.human", stubHuman{}, true, (*authn.HumanVerifier)(nil)))
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

// TestThePhoneSurfaceAndItsFeatureShareOneJudgement holds that SMSCodes present is exactly the authn.phone post and exactly the two phone routes.
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
