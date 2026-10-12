package admin

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
// the required capabilities, the settings store as the one optional one, and
// that this module delivers no capability of its own.
func TestTheDescriptorNamesWhatItRequiresAndDelivers(t *testing.T) {
	m := Module()
	if m.Name != ModuleName || ModuleName != "authn.admin" {
		t.Errorf("module name = %q (constant %q), want authn.admin", m.Name, ModuleName)
	}
	if len(m.Provides) != 0 {
		t.Errorf("Provides = %+v, want none: this module delivers no capability", m.Provides)
	}

	wantRequired := []core.Token{
		(*mount.Mounter)(nil), (*authn.Accounts)(nil), (*authn.AccountAdmin)(nil),
		(*authn.Memberships)(nil), (*audit.Audit)(nil),
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
	if len(m.Requires) != len(wantRequired)+1 || !m.Requires[len(m.Requires)-1].Optional {
		t.Errorf("Requires = %+v, want the settings store as the one optional dependency", m.Requires)
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

// assembly drives a real registry through the module's descriptor.
type assembly struct {
	// audit is the audit capability; nil leaves it out, which must fail the
	// drive with a missing provider before any instance is built.
	audit audit.Audit
}

// drive advances the assembly to Start and returns what the drive returned. It
// hands the Mounter the module mounted its routes on to the run function.
func (a assembly) drive(t *testing.T, run func(m *mounttest.Mounter)) error {
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
	reg.Register(standIn("stand-in.memberships", newMemberships(), (*authn.Memberships)(nil)))
	names = append(names, "stand-in.memberships")
	if a.audit != nil {
		reg.Register(standIn("stand-in.audit", a.audit, (*audit.Audit)(nil)))
		names = append(names, "stand-in.audit")
	}
	reg.Register(standIn("stand-in.settings", newStore(), (*settings.Store)(nil)))
	names = append(names, "stand-in.settings")

	return reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(context.Context, *core.Registry) error {
			run(mounter)
			return nil
		},
	})
}

// TestARealRegistryMountsTheManagementRoutes holds the descriptor end to end:
// the module mounts the account and membership groups and delivers nothing.
func TestARealRegistryMountsTheManagementRoutes(t *testing.T) {
	err := assembly{audit: &recordingAudit{}}.drive(t, func(m *mounttest.Mounter) {
		if _, ok := m.Registered(http.MethodGet, "/auth/admin/accounts"); !ok {
			t.Error("the account group is not mounted")
		}
		if _, ok := m.Registered(http.MethodGet, "/auth/admin/tenants/:tenant/members"); !ok {
			t.Error("the membership group is not mounted")
		}
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}

// TestTheDriveFailsWithoutAnAuditCapability holds the required dependency: the
// module with no audit capability fails before any instance is built.
func TestTheDriveFailsWithoutAnAuditCapability(t *testing.T) {
	err := assembly{}.drive(t, func(*mounttest.Mounter) {})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("the drive = %v, want ErrMissingProvider", err)
	}
}
