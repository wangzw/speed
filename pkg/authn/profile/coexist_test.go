package profile

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/login"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
)

type (
	stubKV          struct{ kv.KV }
	stubMemberships struct{ authn.Memberships }
	stubPasswords   struct{ authn.Passwords }
	stubSignups     struct{ authn.Signups }
	stubSignupState struct{ authn.SignupVerification }
	stubThrottle    struct{ authn.Throttle }
	stubSessions    struct{ authn.Sessions }
	stubMFA         struct{ authn.MFA }
	stubReader      struct{}
)

type stubHuman struct{}

func (stubHuman) Required(authn.HumanScope) bool                                   { return false }
func (stubHuman) Provider(authn.HumanScope) string                                 { return "off" }
func (stubHuman) SiteKey(authn.HumanScope) string                                  { return "" }
func (stubHuman) Action(authn.HumanScope) string                                   { return "" }
func (stubHuman) Verify(context.Context, authn.HumanProof, authn.HumanScope) error { return nil }

func (stubReader) Decode(string, any) error { return nil }

type stubMethod struct{ name string }

func (m stubMethod) Name() string { return m.name }
func (m stubMethod) Describe() authn.MethodDescriptor {
	return authn.MethodDescriptor{Name: m.name, Kind: authn.MethodKindForm}
}

func (stubMethod) Begin(context.Context, authn.Start) (authn.Step, error) {
	return authn.Step{}, nil
}

func (stubMethod) Answer(context.Context, authn.FlowState, authn.StepInput) (authn.Step, error) {
	return authn.Step{}, nil
}

func driveCoexist(t *testing.T, m *mounttest.Mounter) (err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("registering the routes panicked, as a route conflict does: %v", r)
		}
	}()

	modules := []core.Module{
		login.Module(),
		Module(),
		standIn("stand-in.mounter", m, (*mount.Mounter)(nil)),
		standIn("stand-in.kv", stubKV{}, (*kv.KV)(nil)),
		standIn("stand-in.accounts", newAccounts(), (*authn.Accounts)(nil)),
		standIn("stand-in.account-admin", newAccountAdmin(), (*authn.AccountAdmin)(nil)),
		standIn("stand-in.signups", stubSignups{}, (*authn.Signups)(nil)),
		standIn("stand-in.signup-state", stubSignupState{}, (*authn.SignupVerification)(nil)),
		standIn("stand-in.memberships", stubMemberships{}, (*authn.Memberships)(nil)),
		standIn("stand-in.passwords", stubPasswords{}, (*authn.Passwords)(nil)),
		standIn("stand-in.throttle", stubThrottle{}, (*authn.Throttle)(nil)),
		standIn("stand-in.sessions", stubSessions{}, (*authn.Sessions)(nil)),
		standIn("stand-in.mfa", stubMFA{}, (*authn.MFA)(nil)),
		standIn("stand-in.human", stubHuman{}, (*authn.HumanVerifier)(nil)),
		standIn("stand-in.login-method", stubMethod{name: "password"}, (*authn.LoginMethod)(nil)),
	}

	reg := core.New()
	names := make([]string, 0, len(modules))
	for _, m := range modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	// The configuration reader is looked up in Prepare, before any module's New
	// has run, so a host supplies one rather than a stand-in declaring it.
	reg.Register(config.Module())
	reg.Supply(stubReader{}, config.ModuleName)
	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: names,
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
}

// TestTheLoginAndProfileRoutesShareOneEndpoint checks that authn.login and authn.profile mount together without a route conflict.
func TestTheLoginAndProfileRoutesShareOneEndpoint(t *testing.T) {
	m := mounttest.New(t)
	if err := driveCoexist(t, m); err != nil {
		t.Fatalf("the two modules do not coexist on one endpoint: %v", err)
	}
	for _, want := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodPatch, "/api/v1/auth/me"},
		{http.MethodGet, "/api/v1/auth/flows/current"},
		{http.MethodPost, "/api/v1/auth/flows"},
		{http.MethodPost, "/api/v1/auth/flows/authenticated"},
	} {
		if _, ok := m.Registered(want.method, want.path); !ok {
			t.Errorf("%s %s is not mounted, so a module of the pair stood down", want.method, want.path)
		}
	}
}
