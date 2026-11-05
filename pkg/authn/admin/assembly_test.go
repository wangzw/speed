package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authn/internal/mount"
	"github.com/vislake/speed/pkg/authn/internal/mount/mounttest"
	"github.com/vislake/speed/pkg/config"
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

// membersOf is the membership store a drive registers: the one the case
// supplied, or a fresh one.
func membersOf(m *fakeMemberships) *fakeMemberships {
	if m != nil {
		return m
	}
	return newMemberships()
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
		(*authn.Memberships)(nil), (*authn.SignupVerification)(nil), (*audit.Audit)(nil),
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
	if len(m.Requires) != len(wantRequired)+2 || !m.Requires[len(m.Requires)-2].Optional || !m.Requires[len(m.Requires)-1].Optional {
		t.Errorf("Requires = %+v, want the settings store and the logging capability as the optional dependencies", m.Requires)
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
	// roles is the optional member-role read; nil leaves it out, which is a
	// legal assembly with empty role arrays.
	roles authn.MemberRoleReader
	// members is the membership store; nil gets a fresh one.
	members *fakeMemberships
	// decider is the decision seam; nil leaves a legal assembly that refuses
	// every request.
	decider authn.Decider
}

// drive advances the assembly to Start and returns what the drive returned. It
// hands the Mounter the module mounted its routes on to the run function.
func (a assembly) drive(t *testing.T, run func(m *mounttest.Mounter)) error {
	t.Helper()
	mounter := mounttest.New(t)

	reg := core.New()
	reader := authntest.ConfigReader(t, Module(), `{}`)
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(Module())
	names := []string{ModuleName}

	reg.Register(standIn("stand-in.mounter", mounter, (*mount.Mounter)(nil)))
	names = append(names, "stand-in.mounter")
	reg.Register(standIn("stand-in.accounts", newAccounts(), (*authn.Accounts)(nil)))
	names = append(names, "stand-in.accounts")
	reg.Register(standIn("stand-in.account-admin", newAccountAdmin(), (*authn.AccountAdmin)(nil)))
	names = append(names, "stand-in.account-admin")
	reg.Register(standIn("stand-in.memberships", membersOf(a.members), (*authn.Memberships)(nil)))
	names = append(names, "stand-in.memberships")
	if a.roles != nil {
		reg.Register(standIn("stand-in.member-roles", a.roles, (*authn.MemberRoleReader)(nil)))
		names = append(names, "stand-in.member-roles")
	}
	if a.decider != nil {
		reg.Register(standIn("stand-in.decider", a.decider, (*authn.Decider)(nil)))
		names = append(names, "stand-in.decider")
	}
	reg.Register(standIn("stand-in.signup-state", &fakeSignupVerification{}, (*authn.SignupVerification)(nil)))
	names = append(names, "stand-in.signup-state")
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
		if _, ok := m.Registered(http.MethodGet, "/api/v1/system/auth/accounts"); !ok {
			t.Error("the account group is not mounted")
		}
		if _, ok := m.Registered(http.MethodGet, "/api/v1/tenants/:tenant/members"); !ok {
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

// TestAnAssemblyThatDeliversTheMemberRoleReaderUsesIt holds the optional seam
// end to end: the admitted capability is taken up in Init, and the member
// listing carries the roles it answers without the membership store knowing
// about policy.
func TestAnAssemblyThatDeliversTheMemberRoleReaderUsesIt(t *testing.T) {
	members := newMemberships()
	members.put(authn.Membership{Account: userID, Tenant: tenantA, Active: true, Source: authn.ReasonAdmin})
	roles := &fakeMemberRoles{roles: map[authn.AccountID][]string{userID: {"alpha"}}}

	err := assembly{
		audit:   &recordingAudit{},
		roles:   roles,
		members: members,
		decider: &fakeDecider{allow: true},
	}.drive(t, func(m *mounttest.Mounter) {
		m.As(fresh()...)
		rec := m.Do(httptest.NewRequest(http.MethodGet, "/api/v1/tenants/"+tenantA+"/members", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("listing members: status = %d, want 200; body %s", rec.Code, rec.Body.String())
		}
		member := readMemberPage(t, rec.Body.Bytes()).memberByAccount(t, string(userID))
		raw, ok := member["roles"].([]any)
		if !ok || len(raw) != 1 || raw[0] != "alpha" {
			t.Errorf("the member carries roles = %v, want [alpha]", member["roles"])
		}
	})
	if err != nil {
		t.Fatalf("the drive failed: %v", err)
	}
}
