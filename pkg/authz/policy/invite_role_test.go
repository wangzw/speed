package policy

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/provisioning"
	"github.com/vislake/speed/pkg/authz"
)

// TestTheModuleProvidesTheInviteRoleSeam holds the descriptor: the invitation
// role judgement is a provision of this module, so an assembly resolves it
// without a stand-in.
func TestTheModuleProvidesTheInviteRoleSeam(t *testing.T) {
	for _, provision := range Module().Provides {
		if provision.Token == (*authn.InviteRolePolicy)(nil) {
			return
		}
	}
	t.Error("the policy module's descriptor does not provide authn.InviteRolePolicy")
}

// TestTheModuleProvidesTheRoleBinder holds the descriptor: the acceptance's
// in-transaction binding write is a provision of this module, so an assembly
// resolves it without a stand-in.
func TestTheModuleProvidesTheRoleBinder(t *testing.T) {
	for _, provision := range Module().Provides {
		if provision.Token == (*provisioning.RoleBinder)(nil) {
			return
		}
	}
	t.Error("the policy module's descriptor does not provide provisioning.RoleBinder")
}

// holder is a WriteGuard that refuses every binding. It is the host seam's
// refusal the invitation role judgement has to carry as protected.
type holder struct{ denied bool }

func (h holder) Denied(context.Context, string, authz.WriteOp) (bool, error) { return h.denied, nil }

// roleFixture writes the roles and bindings the role-judgement cases read: a
// caller holding role management, an editor role within that held set, a role
// carrying more than the caller holds, an empty role, and a caller holding no
// role-management permission at all.
func roleFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	ctx := t.Context()

	mustPutRole(t, ctx, f, "acme", "manager")
	mustPutGrant(t, ctx, f, "acme", "manager", "tenant_role", "manage")
	mustPutBinding(t, ctx, f, "acme", "acct-1", "manager")

	mustPutRole(t, ctx, f, "acme", "editor")
	mustPutGrant(t, ctx, f, "acme", "editor", "tenant_role", "manage")

	mustPutRole(t, ctx, f, "acme", "power")
	mustPutGrant(t, ctx, f, "acme", "power", "settings", "write")

	mustPutRole(t, ctx, f, "acme", "empty")

	mustPutRole(t, ctx, f, "acme", "reader")
	mustPutGrant(t, ctx, f, "acme", "reader", "audit", "read")
	mustPutBinding(t, ctx, f, "acme", "acct-2", "reader")

	// The acceptance-time judgement replays the inviter's standing, which
	// includes an active membership; every inviter these cases name is a member
	// unless a case says otherwise.
	f.membership = &membershipStub{member: true}

	return f
}

// TestGrantableAnswersTheFourRefusals holds the issue-time judgement: an absent
// role, the platform administrator, a caller short the role-management
// permission, and a role carrying a permission the caller does not hold are told
// apart; a role within the held set passes.
func TestGrantableAnswersTheFourRefusals(t *testing.T) {
	f := roleFixture(t)
	ctx := t.Context()

	if err := f.Grantable(ctx, "acme", "acct-1", "editor"); err != nil {
		t.Errorf("Grantable(editor) = %v, want nil", err)
	}
	if err := f.Grantable(ctx, "acme", "acct-1", "empty"); err != nil {
		t.Errorf("Grantable(empty) = %v, want nil", err)
	}
	if err := f.Grantable(ctx, "acme", "acct-1", "ghost"); !errors.Is(err, authn.ErrRoleUnknown) {
		t.Errorf("Grantable(ghost) = %v, want ErrRoleUnknown", err)
	}
	if err := f.Grantable(ctx, "acme", "acct-1", string(authz.PlatformAdminRole)); !errors.Is(err, authn.ErrRoleProtected) {
		t.Errorf("Grantable(platform-admin) = %v, want ErrRoleProtected", err)
	}
	if err := f.Grantable(ctx, "acme", "acct-1", "power"); !errors.Is(err, authn.ErrRoleProtected) {
		t.Errorf("Grantable(power) = %v, want ErrRoleProtected: the caller holds no settings.write", err)
	}
	if err := f.Grantable(ctx, "acme", "acct-2", "empty"); !errors.Is(err, authn.ErrForbidden) {
		t.Errorf("Grantable without role management = %v, want ErrForbidden", err)
	}
}

// TestGrantableAsksTheWriteGuard holds the host seam: a tenant that protects
// bindings refuses the role through the protected code.
func TestGrantableAsksTheWriteGuard(t *testing.T) {
	f := roleFixture(t)
	f.writeGuard = holder{denied: true}
	if err := f.Grantable(t.Context(), "acme", "acct-1", "editor"); !errors.Is(err, authn.ErrRoleProtected) {
		t.Errorf("Grantable with a refusing write guard = %v, want ErrRoleProtected", err)
	}
}

// TestGrantableFailsClosedWithoutAMembershipChecker holds the assembly rule: an
// invitation an account issues is accepted only after the inviter's standing is
// replayed through the membership checker, so without one the issue itself is
// refused rather than written into an invitation that could never be accepted.
// A host command has no standing to replay and needs none.
func TestGrantableFailsClosedWithoutAMembershipChecker(t *testing.T) {
	f := roleFixture(t)
	f.membership = nil
	err := f.Grantable(t.Context(), "acme", "acct-1", "editor")
	if err == nil || errors.Is(err, authn.ErrForbidden) || errors.Is(err, authn.ErrRoleProtected) {
		t.Errorf("Grantable without a membership checker = %v, want an unavailable judgement", err)
	}
	if err := f.Grantable(t.Context(), "acme", "", "editor"); err != nil {
		t.Errorf("Grantable as a host command without a membership checker = %v, want it admitted", err)
	}
}

// TestGrantableJudgesThePermissionBeforeTheRole holds the order: a caller that
// holds no role-management permission is refused with the same code whether the
// role exists or not, so the two answers cannot be used to learn which role
// names the tenant has.
func TestGrantableJudgesThePermissionBeforeTheRole(t *testing.T) {
	f := roleFixture(t)
	ctx := t.Context()

	if err := f.Grantable(ctx, "acme", "acct-2", "empty"); !errors.Is(err, authn.ErrForbidden) {
		t.Errorf("Grantable(existing role) without role management = %v, want ErrForbidden", err)
	}
	if err := f.Grantable(ctx, "acme", "acct-2", "ghost"); !errors.Is(err, authn.ErrForbidden) {
		t.Errorf("Grantable(absent role) without role management = %v, want ErrForbidden", err)
	}
	if err := f.Grantable(ctx, "acme", "acct-2", "ghost"); errors.Is(err, authn.ErrRoleUnknown) {
		t.Errorf("Grantable(absent role) without role management answered ErrRoleUnknown: " +
			"the caller learns that the role does not exist")
	}
}

// TestGrantableAcceptsAHostCommand holds the host path: a host command issuing
// an invitation has no acting account, and it may bind roles directly, so the
// judgement is the role's existence, the platform-administrator refusal and the
// write guard. The held set is not consulted.
func TestGrantableAcceptsAHostCommand(t *testing.T) {
	f := roleFixture(t)
	ctx := t.Context()

	if err := f.Grantable(ctx, "acme", "", "editor"); err != nil {
		t.Errorf("Grantable(editor) for a host command = %v, want nil", err)
	}
	if err := f.Grantable(ctx, "acme", "", "ghost"); !errors.Is(err, authn.ErrRoleUnknown) {
		t.Errorf("Grantable(ghost) for a host command = %v, want ErrRoleUnknown", err)
	}
	if err := f.Grantable(ctx, "acme", "", string(authz.PlatformAdminRole)); !errors.Is(err, authn.ErrRoleProtected) {
		t.Errorf("Grantable(platform-admin) for a host command = %v, want ErrRoleProtected", err)
	}

	f.writeGuard = holder{denied: true}
	if err := f.Grantable(ctx, "acme", "", "editor"); !errors.Is(err, authn.ErrRoleProtected) {
		t.Errorf("Grantable for a host command under a refusing write guard = %v, want ErrRoleProtected", err)
	}
}

// TestStillGrantableReplaysTheInvitersHeldSet holds the acceptance-time
// judgement: the role has to still exist and every permission it carries has to
// still be within the inviter's held set. A host-issued invitation has no
// inviter, so only the role's existence is checked.
func TestStillGrantableReplaysTheInvitersHeldSet(t *testing.T) {
	f := roleFixture(t)
	ctx := t.Context()

	if err := f.StillGrantable(ctx, "acme", "editor", "acct-1"); err != nil {
		t.Errorf("StillGrantable(editor, acct-1) = %v, want nil", err)
	}
	if err := f.StillGrantable(ctx, "acme", "ghost", "acct-1"); !errors.Is(err, authn.ErrRoleUnknown) {
		t.Errorf("StillGrantable(ghost) = %v, want ErrRoleUnknown", err)
	}
	if err := f.StillGrantable(ctx, "acme", "power", "acct-1"); !errors.Is(err, authn.ErrRoleProtected) {
		t.Errorf("StillGrantable(power, acct-1) = %v, want ErrRoleProtected", err)
	}
	if err := f.StillGrantable(ctx, "acme", "editor", ""); err != nil {
		t.Errorf("StillGrantable(editor, no inviter) = %v, want nil: only the role is checked", err)
	}
	if err := f.StillGrantable(ctx, "acme", "ghost", ""); !errors.Is(err, authn.ErrRoleUnknown) {
		t.Errorf("StillGrantable(ghost, no inviter) = %v, want ErrRoleUnknown", err)
	}
	if len(f.membership.(*membershipStub).calls) != 3 {
		t.Errorf("the membership checker was asked %+v, want one question per inviter",
			f.membership.(*membershipStub).calls)
	}
}

// TestStillGrantableReplaysTheInvitersStanding holds the rest of the replay: a
// membership that is suspended keeps its bindings, so it has to be read
// separately, and an inviter that still holds some permissions but not the
// tenant's role-management permission may no longer have the invitation
// honoured.
func TestStillGrantableReplaysTheInvitersStanding(t *testing.T) {
	t.Run("the inviter is no longer an active member", func(t *testing.T) {
		f := roleFixture(t)
		f.membership = &membershipStub{member: false}
		err := f.StillGrantable(t.Context(), "acme", "editor", "acct-1")
		if !errors.Is(err, authn.ErrForbidden) {
			t.Errorf("StillGrantable with a suspended inviter = %v, want ErrForbidden", err)
		}
	})

	t.Run("the inviter lost role management but still holds a grant", func(t *testing.T) {
		f := roleFixture(t)
		// acct-2 holds the reader role, whose audit.read it therefore holds; it
		// holds no tenant_role.manage.
		err := f.StillGrantable(t.Context(), "acme", "reader", "acct-2")
		if !errors.Is(err, authn.ErrForbidden) {
			t.Errorf("StillGrantable with an inviter that lost role management = %v, want ErrForbidden", err)
		}
	})

	t.Run("the membership read fails", func(t *testing.T) {
		f := roleFixture(t)
		f.membership = &membershipStub{err: errors.New("the account store is down")}
		err := f.StillGrantable(t.Context(), "acme", "editor", "acct-1")
		if err == nil || errors.Is(err, authn.ErrForbidden) || errors.Is(err, authn.ErrRoleProtected) {
			t.Errorf("StillGrantable over a failed membership read = %v, want an unavailable judgement", err)
		}
	})

	t.Run("no membership checker is assembled", func(t *testing.T) {
		f := roleFixture(t)
		f.membership = nil
		err := f.StillGrantable(t.Context(), "acme", "editor", "acct-1")
		if err == nil || errors.Is(err, authn.ErrForbidden) || errors.Is(err, authn.ErrRoleProtected) {
			t.Errorf("StillGrantable without a membership checker = %v, want an unavailable judgement", err)
		}
	})
}

// TestBindRoleTxWritesThroughTheCallersTransaction holds the in-transaction
// binding write: it writes the binding on the handle it is handed, and the caller
// that rolls the transaction back leaves no binding behind.
func TestBindRoleTxWritesThroughTheCallersTransaction(t *testing.T) {
	f := roleFixture(t)
	ctx := t.Context()

	err := f.handle.Transaction(func(tx *gorm.DB) error {
		_, bindErr := f.BindRoleTx(ctx, tx, "acme", "acct-2", "editor")
		return bindErr
	})
	if err != nil {
		t.Fatalf("BindRoleTx: %v", err)
	}
	bindings, err := f.BindingsOf(ctx, "acme", "acct-2")
	if err != nil {
		t.Fatalf("BindingsOf: %v", err)
	}
	if len(bindings) != 2 {
		t.Fatalf("acct-2 holds %d binding(s), want the seeded reader and the new editor", len(bindings))
	}

	// A transaction that rolls back writes nothing.
	if err = f.BindRoleTxRollback(ctx); err != nil {
		t.Fatalf("the rolled-back write: %v", err)
	}
	after, err := f.BindingsOf(ctx, "acme", "acct-1")
	if err != nil {
		t.Fatalf("BindingsOf: %v", err)
	}
	for _, b := range after {
		if b.Role == "editor" && b.Account == "acct-1" {
			t.Error("a rolled-back BindRoleTx left its binding behind")
		}
	}
}

// BindRoleTxRollback runs BindRoleTx for acct-1 and rolls the transaction back.
func (f *fixture) BindRoleTxRollback(ctx context.Context) error {
	rolled := errors.New("roll back")
	err := f.handle.Transaction(func(tx *gorm.DB) error {
		if _, bindErr := f.BindRoleTx(ctx, tx, "acme", "acct-1", "editor"); bindErr != nil {
			return bindErr
		}
		return rolled
	})
	if !errors.Is(err, rolled) {
		return err
	}
	return nil
}
