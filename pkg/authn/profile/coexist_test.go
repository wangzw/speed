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

// The capabilities another module delivers exclusively that this test's
// assembly needs but that no case reaches into: each stands in for one, carrying
// a nil embedded interface so that a call the drive should not make panics
// rather than passing silently.
type (
	stubKV          struct{ kv.KV }
	stubMemberships struct{ authn.Memberships }
	stubPasswords   struct{ authn.Passwords }
	stubThrottle    struct{ authn.Throttle }
	stubSessions    struct{ authn.Sessions }
	stubReader      struct{}
)

// Decode reads nothing: every value the login module reads from the
// configuration keeps its zero value, which the module's own defaults cover.
func (stubReader) Decode(string, any) error { return nil }

// stubMethod is a login method that the drive registers and never calls: the
// engine only needs a name to take the method up.
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

// driveCoexist drives authn.login and authn.profile together, on one Mounter and
// with every capability each requires present, to the stage where their routes
// are registered. A route both modules register makes gin panic where the second
// one is mounted; the panic is caught and returned, so a conflict is a failure
// of the calling test rather than the end of its binary.
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
		standIn("stand-in.memberships", stubMemberships{}, (*authn.Memberships)(nil)),
		standIn("stand-in.passwords", stubPasswords{}, (*authn.Passwords)(nil)),
		standIn("stand-in.throttle", stubThrottle{}, (*authn.Throttle)(nil)),
		standIn("stand-in.sessions", stubSessions{}, (*authn.Sessions)(nil)),
		standIn("stand-in.mfa", &fakeMFA{}, (*authn.MFA)(nil)),
		standIn("stand-in.login-method", stubMethod{name: "password"}, (*authn.LoginMethod)(nil)),
	}

	reg := core.New()
	names := make([]string, 0, len(modules))
	for _, m := range modules {
		reg.Register(m)
		names = append(names, m.Name)
	}
	// The configuration reader is looked up in Prepare, before any module's New
	// has run, so it is supplied the way a host supplies one and not declared by
	// a stand-in.
	reg.Register(config.Module())
	reg.Supply(stubReader{}, config.ModuleName)
	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: names,
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
}

// TestTheLoginAndProfileRoutesShareOneEndpoint holds that authn.login and
// authn.profile can be taken in together and mount onto one endpoint: the drive
// registers both modules' routes under one base path, and no route of one is a
// route of the other, which is what a conflicting registration would be.
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
