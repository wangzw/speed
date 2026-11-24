package login_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/authn/login"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

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

type (
	stubKV          struct{ kv.KV }
	stubAccounts    struct{ authn.Accounts }
	stubAdmin       struct{ authn.AccountAdmin }
	stubSignups     struct{ authn.Signups }
	stubSignupState struct{ authn.SignupVerification }
	stubPasswords   struct{ authn.Passwords }
	stubMemberships struct{ authn.Memberships }
	stubThrottle    struct{ authn.Throttle }
	stubSessions    struct{ authn.Sessions }
	stubMFA         struct{ authn.MFA }
)

type stubHuman struct{}

var _ authn.HumanVerifier = stubHuman{}

func (stubHuman) Required(authn.HumanScope) bool                                   { return false }
func (stubHuman) Provider(authn.HumanScope) string                                 { return "off" }
func (stubHuman) SiteKey(authn.HumanScope) string                                  { return "" }
func (stubHuman) Action(authn.HumanScope) string                                   { return "" }
func (stubHuman) Verify(context.Context, authn.HumanProof, authn.HumanScope) error { return nil }

func standIn(name string, instance any, exclusive bool, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: exclusive}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

func methodModule(i int, name string) core.Module {
	return standIn(fmt.Sprintf("stand-in.method.%d.%s", i, name), stubMethod{name: name}, false, (*authn.LoginMethod)(nil))
}

func driveEngine(t *testing.T, mounter *mounttest.Mounter, names []string, methods []string) error {
	t.Helper()
	return driveEngineWith(t, mounter, names, methods, true)
}

func driveEngineWith(t *testing.T, mounter *mounttest.Mounter, names []string, methods []string, human bool) error {
	t.Helper()
	reader := loadConfig(t, "{}")

	reg := core.New()
	reg.Register(login.Module())
	participants := []string{login.ModuleName}

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
	if human {
		register(standIn("stand-in.human", stubHuman{}, true, (*authn.HumanVerifier)(nil)))
	}
	for i, name := range methods {
		register(methodModule(i, name))
	}
	_ = names

	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageInit,
		Participants: participants,
		Action:       func(context.Context, *core.Registry) error { return nil },
	})
}

// TestNoMethodFailsTheDrive holds that an assembly with no login method fails with ErrNoLoginMethod.
func TestNoMethodFailsTheDrive(t *testing.T) {
	err := driveEngine(t, mounttest.New(t), nil, nil)
	if !errors.Is(err, authn.ErrNoLoginMethod) {
		t.Errorf("the drive returned %v, want ErrNoLoginMethod", err)
	}
}

// TestTwoMethodsOfOneNameFailTheDrive holds that a duplicate method name fails with ErrMethodConflict, naming both.
func TestTwoMethodsOfOneNameFailTheDrive(t *testing.T) {
	err := driveEngine(t, mounttest.New(t), nil, []string{"password", "password"})
	if !errors.Is(err, authn.ErrMethodConflict) {
		t.Errorf("the drive returned %v, want ErrMethodConflict", err)
	}
}

// TestOneMethodRegistersTheRoutes holds that a real registry builds the engine, takes up the method and registers the routes with their access levels.
func TestOneMethodRegistersTheRoutes(t *testing.T) {
	m := mounttest.New(t)
	err := driveEngine(t, m, nil, []string{"password"})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
	want := map[string]bool{
		"GET /api/v1/auth/methods":              true,
		"POST /api/v1/auth/register":            true,
		"POST /api/v1/auth/flows":               true,
		"POST /api/v1/auth/flows/answer":        true,
		"POST /api/v1/auth/flows/authenticated": true,
		"GET /api/v1/auth/flows/current":        true,
		"DELETE /api/v1/auth/flows/current":     true,
	}
	got := map[string]bool{}
	for _, reg := range m.Registrations() {
		for _, method := range reg.Methods {
			got[method+" "+reg.Path] = true
		}
	}
	for key := range want {
		if !got[key] {
			t.Errorf("the engine registered no route %q; it has %v", key, got)
		}
	}
	authenticated, ok := m.Registered(http.MethodPost, "/api/v1/auth/flows/authenticated")
	if !ok || !authenticated.Impersonable {
		t.Errorf("POST /api/v1/auth/flows/authenticated is not registered impersonable: %+v", authenticated)
	}
	for _, path := range []string{"/api/v1/auth/flows", "/api/v1/auth/flows/answer"} {
		reg, ok := m.Registered(http.MethodPost, path)
		if !ok {
			t.Fatalf("POST %s is not registered", path)
		}
		if reg.Impersonable {
			t.Errorf("POST %s is registered impersonable, and it carries no identity", path)
		}
	}
}

// TestTheDescriptorRequiresWhatTheEngineNeeds holds that exclusive capabilities are required and the settings store and logger optional.
func TestTheDescriptorRequiresWhatTheEngineNeeds(t *testing.T) {
	m := login.Module()
	if m.Name != login.ModuleName || login.ModuleName != "authn.login" {
		t.Errorf("module name = %q, want authn.login", m.Name)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("Provides = %+v, want the login-method switch alone", m.Provides)
	}
	if p := m.Provides[0]; p.Token != core.Token((*authn.LoginMethodSwitch)(nil)) || !p.Exclusive {
		t.Errorf("Provides[0] = %+v, want authn.LoginMethodSwitch, exclusive: the engine owns the switch", p)
	}
	required := map[core.Token]bool{}
	optional := map[core.Token]bool{}
	for _, r := range m.Requires {
		if r.Optional {
			optional[r.Token] = true
		} else {
			required[r.Token] = true
		}
	}
	for _, tk := range []core.Token{
		(*mount.Mounter)(nil), (*kv.KV)(nil), (*authn.Accounts)(nil), (*authn.AccountAdmin)(nil),
		(*authn.Signups)(nil), (*authn.SignupVerification)(nil), (*authn.Passwords)(nil),
		(*authn.Memberships)(nil), (*authn.Throttle)(nil),
		(*authn.Sessions)(nil), (*authn.MFA)(nil), (*authn.HumanVerifier)(nil),
	} {
		if !required[tk] {
			t.Errorf("Requires is missing %v", tk)
		}
	}
	for _, tk := range []core.Token{
		(*settings.Store)(nil), (*log.Logger)(nil),
	} {
		if !optional[tk] {
			t.Errorf("Requires does not hold %v as optional", tk)
		}
	}
	if len(m.Requires) != len(required)+2 || !m.Requires[len(m.Requires)-2].Optional ||
		!m.Requires[len(m.Requires)-1].Optional {
		t.Errorf("Requires = %+v, want the settings store and the logging capability as the optional "+
			"dependencies", m.Requires)
	}
}

// TestTheDriveFailsWithoutAHumanVerifier holds that an assembly without the required gate fails in resolution.
func TestTheDriveFailsWithoutAHumanVerifier(t *testing.T) {
	err := driveEngineWith(t, mounttest.New(t), nil, []string{"password"}, false)
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("the drive returned %v, want ErrMissingProvider", err)
	}
	if !strings.Contains(err.Error(), "HumanVerifier") {
		t.Errorf("the failure %q does not name the missing human-verification gate", err.Error())
	}
}

// TestImportingThePackageRegistersTheModule holds the init registration.
func TestImportingThePackageRegistersTheModule(t *testing.T) {
	if _, ok := core.ProcessRegistry.Lookup(login.ModuleName); !ok {
		t.Fatalf("the process registry holds no module named %s", login.ModuleName)
	}
}
