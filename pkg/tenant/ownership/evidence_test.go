package ownership_test

import (
	"errors"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/personal"
)

func TestTransferOwnerGrantsTenantAdminToAMemberThatDidNotHoldIt(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		if before := a.bindingsOf(t, "acme", target); len(before) != 0 {
			t.Fatalf("the target holds %+v before the transfer, want no binding", before)
		}

		a.transfer(t, "acme", target.ID.String())
		after := a.bindingsOf(t, "acme", target)
		if len(after) != 1 || after[0].Role != authz.Role(authz.TenantAdminRole) {
			t.Fatalf("the target holds %+v after the transfer, want exactly tenant-admin", after)
		}
	})
}

func TestTransferOwnerKeepsThePreviousOwnerAnActiveMemberHoldingTenantAdmin(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		if held := a.bindingsOf(t, "acme", owner); len(held) != 1 ||
			held[0].Role != authz.Role(authz.TenantAdminRole) {
			t.Fatalf("the previous owner holds %+v before the transfer, want tenant-admin", held)
		}

		a.transfer(t, "acme", target.ID.String())
		held := a.bindingsOf(t, "acme", owner)
		if len(held) != 1 || held[0].Role != authz.Role(authz.TenantAdminRole) {
			t.Errorf("the previous owner holds %+v after the transfer, want tenant-admin", held)
		}
		m, found, err := a.memberships.Membership(t.Context(), "acme", owner.ID)
		if err != nil || !found || !m.Active {
			t.Errorf("the previous owner's membership is %+v (found=%t err=%v), want the active one",
				m, found, err)
		}
	})
}

func TestTransferOwnerKeepsThePreviousOwnersOtherBindings(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		target := a.user(t, "bob")
		a.organization(t, "acme", "acme-org", owner)
		a.join(t, "acme", target)

		if err := a.manager.PutRole(t.Context(), authz.RoleRecord{
			Tenant: "acme", Role: "auditor", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("creating the auditor role: %v", err)
		}
		if err := a.manager.PutBinding(t.Context(), authz.Binding{
			Tenant: "acme", Account: owner.ID.String(), Role: "auditor", GrantedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("binding the previous owner to auditor: %v", err)
		}
		if held := a.bindingsOf(t, "acme", owner); len(held) != 2 {
			t.Fatalf("the previous owner holds %+v before the transfer, want tenant-admin and auditor", held)
		}

		a.transfer(t, "acme", target.ID.String())
		roles := make(map[authz.Role]bool)
		for _, held := range a.bindingsOf(t, "acme", owner) {
			roles[held.Role] = true
		}
		if len(roles) != 2 || !roles[authz.Role(authz.TenantAdminRole)] || !roles["auditor"] {
			t.Errorf("the previous owner holds %+v after the transfer, want tenant-admin and auditor", roles)
		}
	})
}

func TestDeleteOrganizationRollsBackAFailureAtEachCleanupStep(t *testing.T) {
	cases := map[string]string{
		"the tombstone": `CREATE TRIGGER refuse_roster_tombstone BEFORE UPDATE ON tenants
			BEGIN SELECT RAISE(ABORT, 'induced failure'); END`,
		"the policy rows": `CREATE TRIGGER refuse_policy_delete BEFORE DELETE ON authz_grants
			BEGIN SELECT RAISE(ABORT, 'induced failure'); END`,
		"the memberships": `CREATE TRIGGER refuse_membership_delete BEFORE DELETE ON authn_memberships
			BEGIN SELECT RAISE(ABORT, 'induced failure'); END`,
	}
	for name, trigger := range cases {
		t.Run(name, func(t *testing.T) {
			withAssembly(t, func(t *testing.T, a *assembly) {
				owner := a.user(t, "alice")
				a.organization(t, "acme", "acme-org", owner)
				if _, err := a.seeder.SeedGrants(t.Context(), "acme", authz.TenantAdminRole,
					[]authz.Grant{{Resource: "deal", Action: "read"}}); err != nil {
					t.Fatalf("seeding a grant: %v", err)
				}
				if err := a.handle.Exec(trigger).Error; err != nil {
					t.Fatalf("creating the trigger that refuses a cleanup step: %v", err)
				}

				if err := a.writer.DeleteOrganization(t.Context(), "acme", ""); err == nil {
					t.Fatal("the deletion succeeded though a cleanup step failed")
				}
				if status, deletedAt := a.rosterRow(t, "acme"); status != tenant.StatusActive || deletedAt != nil {
					t.Errorf("the rollback left status %q and deleted_at %v, want the active row", status, deletedAt)
				}
				for table, want := range map[string]int64{
					"authn_memberships": 1,
					"authz_roles":       1,
					"authz_bindings":    1,
					"authz_grants":      1,
				} {
					if n := a.count(t, table, "tenant_id = ?", "acme"); n != want {
						t.Errorf("the rollback left %d row(s) in %s, want %d", n, table, want)
					}
				}
			})
		})
	}
}

func TestADeletedTenantIsNotRecreatedWithItsIdentifierOrName(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		owner := a.user(t, "alice")
		a.organization(t, "acme", "acme-org", owner)
		if err := a.writer.DeleteOrganization(t.Context(), "acme", ""); err != nil {
			t.Fatalf("deleting the organization: %v", err)
		}

		if err := a.catalog.Create(t.Context(), tenant.Tenant{
			ID: "acme", Name: "another", Status: tenant.StatusActive,
		}); !errors.Is(err, tenant.ErrTenantExists) {
			t.Errorf("registering the deleted identifier gave %v, want ErrTenantExists", err)
		}
		if err := a.catalog.Create(t.Context(), tenant.Tenant{
			ID: "other", Name: "acme-org", Status: tenant.StatusActive,
		}); !errors.Is(err, tenant.ErrNameConflict) {
			t.Errorf("registering the deleted name gave %v, want ErrNameConflict", err)
		}
		if err := a.catalog.Create(t.Context(), tenant.Tenant{
			ID: "fresh", Name: "fresh-org", Status: tenant.StatusActive,
		}); err != nil {
			t.Errorf("registering an unused identifier and name gave %v, want success", err)
		}
	})
}

func TestAPersonalOrganizationDoesNotProtectItsAccount(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		suspended := a.user(t, "alice")
		deleted := a.user(t, "bob")

		for _, account := range []authn.Account{suspended, deleted} {
			personalID := personal.TenantID(account.ID)
			held := a.held(t, string(personalID))
			if held.OwnerAccount != account.ID.String() {
				t.Fatalf("the personal tenant %q has owner %q, want %q",
					personalID, held.OwnerAccount, account.ID)
			}
		}

		if err := a.admin.SetStatus(t.Context(), suspended.ID, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Errorf("suspending an account that owns only its personal organization gave %v, want success", err)
		}
		if err := a.admin.Delete(t.Context(), deleted.ID, authn.ReasonAdmin); err != nil {
			t.Errorf("deleting an account that owns only its personal organization gave %v, want success", err)
		}
	})
}

func TestAnAccountThatOwnsNothingIsUnaffected(t *testing.T) {
	withAssembly(t, func(t *testing.T, a *assembly) {
		created, err := a.admin.Create(t.Context(), authn.CreateAccount{
			Kind: authn.KindService, DisplayName: "robot", Via: authn.ReasonAdmin,
		})
		if err != nil {
			t.Fatalf("creating the service account: %v", err)
		}
		if err := a.admin.SetStatus(t.Context(), created.ID, authn.StatusDisabled, authn.ReasonAdmin); err != nil {
			t.Errorf("suspending an account that owns nothing gave %v, want success", err)
		}
		if err := a.admin.Delete(t.Context(), created.ID, authn.ReasonAdmin); err != nil {
			t.Errorf("deleting an account that owns nothing gave %v, want success", err)
		}
	})
}
