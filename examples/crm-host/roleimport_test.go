package main

import (
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestTheRoleImportCarriesTheHostsFormerRoleTable is the case for the role
// migration's counts: the host's own member_roles table held a member's role,
// and a role is a policy binding now, so each row it can carry becomes a binding
// in its tenant's domain while the tenant's four roles and their grant rows are
// provisioned with it. The old table is left in place.
func TestTheRoleImportCarriesTheHostsFormerRoleTable(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		globex := c.createTenant("globex")
		alice := c.createUser("alice")
		bob := c.createUser("bob")

		c.insertMemberRole(acme.ID, alice.ID, roleOwner)
		c.insertMemberRole(globex.ID, alice.ID, roleRep)
		c.insertMemberRole(acme.ID, bob.ID, roleViewer)

		var rep dataImportReport
		c.resultOf(c.mustRun("", "role", "import"), &rep)
		if rep.Created != 3 || rep.Skipped != 0 || rep.Failed != 0 {
			t.Fatalf("the import answered created %d skipped %d failed %d, want 3, 0, 0",
				rep.Created, rep.Skipped, rep.Failed)
		}
		if got := c.count("SELECT count(*) FROM authz_bindings"); got != 3 {
			t.Errorf("the import wrote %d bindings, want one per row it carried", got)
		}
		if got := c.text("SELECT role FROM authz_bindings WHERE tenant_id = ? AND account_id = ?", acme.ID, alice.ID); got != roleOwner {
			t.Errorf("alice's role in acme reads back as %q, want owner", got)
		}
		if got := c.text("SELECT role FROM authz_bindings WHERE tenant_id = ? AND account_id = ?", globex.ID, alice.ID); got != roleRep {
			t.Errorf("alice's role in globex reads back as %q, want rep", got)
		}
		// Each tenant the run touched has its four roles provisioned and its
		// grant rows written with them.
		if got := c.count("SELECT count(*) FROM authz_roles WHERE tenant_id IN (?, ?)", acme.ID, globex.ID); got != 8 {
			t.Errorf("the import left %d role rows for the two tenants, want the four roles of each", got)
		}
		if got := c.count("SELECT count(*) FROM authz_grants WHERE tenant_id IN (?, ?)", acme.ID, globex.ID); got == 0 {
			t.Errorf("the import wrote no grant rows for the tenants it provisioned")
		}
		// The old table is left in place, untouched.
		if got := c.count("SELECT count(*) FROM member_roles"); got != 3 {
			t.Errorf("member_roles holds %d rows after the import, want the 3 it started with", got)
		}
		if got := c.intFrom("SELECT COUNT(*) FROM audit_entries WHERE action = 'role.import'"); got != 1 {
			t.Errorf("the import wrote %d journal rows, want one", got)
		}
	})
}

// TestTheRoleImportIsIdempotent is the case for a repeated run: every row whose
// binding the tenant's policy already holds is left as it is and counted
// skipped, so a run may be repeated without reverting a role an administrator
// raised since, and a second run adds no binding.
func TestTheRoleImportIsIdempotent(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.insertMemberRole(acme.ID, alice.ID, roleOwner)

		var first dataImportReport
		c.resultOf(c.mustRun("", "role", "import"), &first)
		if first.Created != 1 {
			t.Fatalf("the first run created %d, want 1", first.Created)
		}

		var again dataImportReport
		c.resultOf(c.mustRun("", "role", "import"), &again)
		if again.Skipped != 1 || again.Created != 0 || again.Resumed != 0 || again.Failed != 0 {
			t.Errorf("the second run answered created %d resumed %d skipped %d failed %d, want 0, 0, 1, 0",
				again.Created, again.Resumed, again.Skipped, again.Failed)
		}
		if got := c.count("SELECT count(*) FROM authz_bindings"); got != 1 {
			t.Errorf("the second run changed the binding count to %d, want 1", got)
		}
	})
}

// TestTheRoleImportDryRunWritesNoPolicy is the case for a --dry-run report: it
// reads the same state and answers the same counts without writing any binding,
// role or grant row, nor any journal row.
func TestTheRoleImportDryRunWritesNoPolicy(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.insertMemberRole(acme.ID, alice.ID, roleOwner)

		var dry dataImportReport
		c.resultOf(c.mustRun("", "role", "import", "--dry-run"), &dry)
		if !dry.DryRun || dry.Created != 1 || dry.Failed != 0 {
			t.Fatalf("the dry run answered dry_run %v created %d failed %d, want true, 1, 0",
				dry.DryRun, dry.Created, dry.Failed)
		}
		if got := c.count("SELECT count(*) FROM authz_bindings"); got != 0 {
			t.Errorf("the dry run wrote %d bindings, want none", got)
		}
		if got := c.count("SELECT count(*) FROM authz_roles"); got != 0 {
			t.Errorf("the dry run wrote %d role rows, want none", got)
		}
		if got := c.count("SELECT count(*) FROM authz_grants"); got != 0 {
			t.Errorf("the dry run wrote %d grant rows, want none", got)
		}
		if got := c.count("SELECT count(*) FROM audit_entries WHERE action = 'role.import'"); got != 0 {
			t.Errorf("the dry run wrote %d journal rows, want none", got)
		}
	})
}

// TestTheRoleImportResumesAHalfWrittenRole is the case for the one row a re-run
// finishes: an account already a member of the tenant — an earlier command wrote
// the membership and died before writing the role — whose binding is missing.
// The run writes the missing role and counts the row resumed rather than
// created, and a further run skips it.
func TestTheRoleImportResumesAHalfWrittenRole(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.insertMemberRole(acme.ID, alice.ID, roleRep)
		// An earlier command wrote alice's membership and died before writing
		// her role: granting writes both, so the binding is removed to leave the
		// membership behind.
		c.grant(acme, alice, roleViewer)
		c.exec("DELETE FROM authz_bindings WHERE tenant_id = ? AND account_id = ?", acme.ID, alice.ID)

		var rep dataImportReport
		c.resultOf(c.mustRun("", "role", "import"), &rep)
		if rep.Resumed != 1 || rep.Created != 0 || rep.Skipped != 0 || rep.Failed != 0 {
			t.Fatalf("the run answered created %d resumed %d skipped %d failed %d, want 0, 1, 0, 0",
				rep.Created, rep.Resumed, rep.Skipped, rep.Failed)
		}
		if got := c.text("SELECT role FROM authz_bindings WHERE tenant_id = ? AND account_id = ?", acme.ID, alice.ID); got != roleRep {
			t.Errorf("the missing role was not written; the row holds %q, want %q", got, roleRep)
		}

		var again dataImportReport
		c.resultOf(c.mustRun("", "role", "import"), &again)
		if again.Skipped != 1 || again.Created != 0 || again.Resumed != 0 {
			t.Errorf("the run over the finished state answered created %d resumed %d skipped %d, want 0, 0, 1",
				again.Created, again.Resumed, again.Skipped)
		}
	})
}

// TestTheRoleImportSkipsRowsItCannotCarry is the case for the rows the migration
// cannot carry: a tenant nobody registered and an identifier under the static
// prefix are each counted failed and the run goes on, writing the rows it can.
func TestTheRoleImportSkipsRowsItCannotCarry(t *testing.T) {
	t.Parallel()
	forImportStacks(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.insertMemberRole(acme.ID, alice.ID, roleOwner)
		// A row whose tenant nobody registered.
		c.insertMemberRole("t_gone_"+c.suffix, alice.ID, roleViewer)
		// A row naming an account under the static prefix, which no member is.
		c.insertMemberRole(acme.ID, authn.StaticPrefix+"someone", roleViewer)

		var rep dataImportReport
		c.resultOf(c.mustRun("", "role", "import"), &rep)
		if rep.Created != 1 || rep.Failed != 2 {
			t.Fatalf("the import answered created %d failed %d, want 1, 2", rep.Created, rep.Failed)
		}
		for _, reason := range []string{"tenant-not-in-roster", "identifier-static"} {
			if got := rep.Reasons[reason]; got != 1 {
				t.Errorf("the run reported reason %q %d times, want 1; reasons %v", reason, got, rep.Reasons)
			}
		}
		// The one row it could carry was written despite the failures.
		if got := c.count("SELECT count(*) FROM authz_bindings"); got != 1 {
			t.Errorf("the import wrote %d bindings, want the 1 it could carry", got)
		}
	})
}
