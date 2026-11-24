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

// TestTheModuleProvidesTheInviteRoleSeam checks that the module provides the invite role seam.
func TestTheModuleProvidesTheInviteRoleSeam(t *testing.T) {
	for _, provision := range Module().Provides {
		if provision.Token == (*authn.InviteRolePolicy)(nil) {
			return
		}
	}
	t.Error("the policy module's descriptor does not provide authn.InviteRolePolicy")
}

// TestTheModuleProvidesTheRoleBinder checks that the module provides the role binder.
func TestTheModuleProvidesTheRoleBinder(t *testing.T) {
	for _, provision := range Module().Provides {
		if provision.Token == (*provisioning.RoleBinder)(nil) {
			return
		}
	}
	t.Error("the policy module's descriptor does not provide provisioning.RoleBinder")
}

type holder struct{ denied bool }

func (h holder) Denied(context.Context, string, authz.WriteOp) (bool, error) { return h.denied, nil }

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

	f.membership = &membershipStub{member: true}

	return f
}

// TestGrantableAnswersTheFourRefusals checks that grantable answers the four refusals.
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

// TestGrantableAsksTheWriteGuard checks that grantable asks the write guard.
func TestGrantableAsksTheWriteGuard(t *testing.T) {
	f := roleFixture(t)
	f.writeGuard = holder{denied: true}
	if err := f.Grantable(t.Context(), "acme", "acct-1", "editor"); !errors.Is(err, authn.ErrRoleProtected) {
		t.Errorf("Grantable with a refusing write guard = %v, want ErrRoleProtected", err)
	}
}

// TestGrantableFailsClosedWithoutAMembershipChecker checks that grantable fails closed without a membership checker.
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

// TestGrantableJudgesThePermissionBeforeTheRole checks that grantable judges the permission before the role.
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

// TestGrantableAcceptsAHostCommand checks that grantable accepts a host command.
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

// TestStillGrantableReplaysTheInvitersHeldSet checks that still grantable replays the inviters held set.
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

// TestStillGrantableReplaysTheInvitersStanding checks that still grantable replays the inviters standing.
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

// TestBindRoleTxWritesThroughTheCallersTransaction checks that bind role TX writes through the callers transaction.
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
