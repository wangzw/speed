package profile

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
)

// standIn is a module that delivers instance for the tokens named, the way
// another module of the directory delivers its capability exclusively.
func standIn(name string, instance any, tokens ...core.Token) core.Module {
	provides := make([]core.Provision, len(tokens))
	for i, tk := range tokens {
		provides[i] = core.Provision{Token: tk, Exclusive: true}
	}
	return core.Module{
		Name:     name,
		Provides: provides,
		New:      func(context.Context, *core.Registry) (any, error) { return instance, nil },
	}
}

// TestTheDescriptorNamesWhatItRequiresAndDelivers holds the descriptor's shape:
// the required capabilities, the optional ones, and that this module delivers no
// capability of its own.
func TestTheDescriptorNamesWhatItRequiresAndDelivers(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.profile" {
		t.Errorf("module name = %q (constant %q), want authn.profile", m.Name, ModuleName)
	}
	if len(m.Provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", m.Provides)
	}

	wantRequired := []core.Token{
		(*mount.Mounter)(nil), (*authn.Accounts)(nil), (*authn.AccountAdmin)(nil), (*authn.MFA)(nil),
	}
	var required []core.Token
	for _, r := range m.Requires {
		if !r.Optional {
			required = append(required, r.Token)
		}
	}
	if len(required) != len(wantRequired) {
		t.Fatalf("Requires = %+v, want the required capabilities %v", m.Requires, wantRequired)
	}
	for i, tk := range wantRequired {
		if required[i] != tk {
			t.Errorf("Required[%d] = %v, want %v", i, required[i], tk)
		}
	}

	wantOptional := []core.Token{(*settings.Store)(nil), (*audit.Audit)(nil), (*log.Logger)(nil)}
	var optional []core.Token
	for _, r := range m.Requires {
		if r.Optional {
			optional = append(optional, r.Token)
		}
	}
	if len(optional) != len(wantOptional) {
		t.Fatalf("Requires = %+v, want the optional capabilities %v", m.Requires, wantOptional)
	}
	for i, tk := range wantOptional {
		if optional[i] != tk {
			t.Errorf("Optional[%d] = %v, want %v", i, optional[i], tk)
		}
	}
}

// TestPrepareStatesEnabled holds the stance.
func TestPrepareStatesEnabled(t *testing.T) {
	got, err := Module().Prepare(t.Context(), core.New())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if got != (core.Enablement{State: core.StateEnabled}) {
		t.Errorf("Prepare = %+v, want the enabled stance alone", got)
	}
}

// TestImportingThePackageRegistersTheModuleWithTheProcess holds the init.
func TestImportingThePackageRegistersTheModuleWithTheProcess(t *testing.T) {
	if _, ok := core.ProcessRegistry.Lookup(ModuleName); !ok {
		t.Fatalf("the process registry holds no module named %s", ModuleName)
	}
}

// drive advances the assembly to Start through the module's descriptor with
// every required capability present and the two optional ones the caller names.
func drive(t *testing.T, run func(m *mounttest.Mounter), omit ...leaveOut) error {
	t.Helper()
	mounter := mounttest.New(t)
	reg := core.New()
	reg.Register(Module())
	names := []string{ModuleName}

	reg.Register(standIn("stand-in.mounter", mounter, (*mount.Mounter)(nil)))
	names = append(names, "stand-in.mounter")
	reg.Register(standIn("stand-in.accounts", newAccounts(), (*authn.Accounts)(nil)))
	names = append(names, "stand-in.accounts")
	reg.Register(standIn("stand-in.account-admin", newAccountAdmin(), (*authn.AccountAdmin)(nil)))
	names = append(names, "stand-in.account-admin")

	left := map[leaveOut]bool{}
	for _, o := range omit {
		left[o] = true
	}
	if !left[noMFA] {
		reg.Register(standIn("stand-in.mfa", &fakeMFA{}, (*authn.MFA)(nil)))
		names = append(names, "stand-in.mfa")
	}
	if !left[noStore] {
		reg.Register(standIn("stand-in.settings", &fakeStore{window: defaultRecentWindow}, (*settings.Store)(nil)))
		names = append(names, "stand-in.settings")
	}
	if !left[noAudit] {
		reg.Register(standIn("stand-in.audit", &recordingAudit{}, (*audit.Audit)(nil)))
		names = append(names, "stand-in.audit")
	}

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(context.Context, *core.Registry) error {
			run(mounter)
			return nil
		},
	})
}

// TestARealRegistryMountsTheProfileRoutes holds the descriptor end to end: the
// module mounts GET /me as impersonable and PATCH /me as not, both guarded.
func TestARealRegistryMountsTheProfileRoutes(t *testing.T) {
	err := drive(t, func(m *mounttest.Mounter) {
		get, ok := m.Registered(http.MethodGet, "/auth/me")
		if !ok {
			t.Fatal("GET /auth/me is not mounted")
		}
		if get.Kind != mounttest.Guarded {
			t.Errorf("GET /auth/me kind = %q, want guarded", get.Kind)
		}
		if !get.Impersonable {
			t.Error("GET /auth/me is not registered impersonable")
		}
		patch, ok := m.Registered(http.MethodPatch, "/auth/me")
		if !ok {
			t.Fatal("PATCH /auth/me is not mounted")
		}
		if patch.Kind != mounttest.Guarded {
			t.Errorf("PATCH /auth/me kind = %q, want guarded", patch.Kind)
		}
		if patch.Impersonable {
			t.Error("PATCH /auth/me is registered impersonable, and a write of the profile is not impersonable")
		}
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// TestTheDriveFailsWithoutTheMultiFactorComposition holds the required
// dependency: a write holds the caller to the account's factor freshness, so a
// drive with no multi-factor composition fails before any instance is built.
func TestTheDriveFailsWithoutTheMultiFactorComposition(t *testing.T) {
	err := drive(t, func(*mounttest.Mounter) {}, noMFA)
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("the drive = %v, want ErrMissingProvider", err)
	}
}
