package persist

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

// clear empties both policy tables across tenants, so one case can run several
// removals from a known state without a second handle. It goes through the
// cross-tenant mark, which the seed does too.
func clear(t *testing.T, handle *gorm.DB) {
	t.Helper()
	for _, table := range []string{"authz_grants", "authz_bindings"} {
		if err := handle.WithContext(crossing()).Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("clearing %s: %v", table, err)
		}
	}
}

// The filter of a p rule reads the rule's own positions, and every position a
// caller may name is legal while one past the rule is refused rather than run.
// The refusal is checked for both the lower and the upper bound, because a
// negative position that reached the column lookup would panic on the index
// rather than return.
func TestAFilteredRemovalReadsEveryFieldPosition(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)

		cases := []struct {
			name    string
			index   int
			values  []string
			wantErr bool
		}{
			{name: "the first position, the role", index: 0, values: []string{"roleA"}},
			{name: "the last position, the action", index: 3, values: []string{"read"}},
			{name: "one past the last position", index: 4, values: []string{"x"}, wantErr: true},
			{name: "below the first position", index: -1, values: []string{"x"}, wantErr: true},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				clear(t, handle)
				seed(t, handle)

				err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("p", "p", c.index, c.values...)
				if c.wantErr {
					if err == nil {
						t.Fatalf("a filter reaching position %d was run rather than refused", c.index)
					}
					if n := count(t, handle, "authz_grants"); n != 2 {
						t.Errorf("the refused removal left %d row(s), want both", n)
					}
					return
				}
				if err != nil {
					t.Fatalf("a filter on position %d was refused: %v", c.index, err)
				}
				// Both tenants hold a grant, and only acme's matches, so the
				// removal reaches one row and the other tenant's stays.
				if n := count(t, handle, "authz_grants"); n != 1 {
					t.Errorf("the removal left %d row(s), want the other tenant's alone", n)
				}
			})
		}
	})
}

// A filter value that is empty matches any value in its position, so a removal
// naming an empty value deletes every one of the caller's rows and no other
// tenant's. Dropping that reading would turn the removal into a match on an
// empty column, which reaches nothing.
func TestAFilteredRemovalWithAnEmptyValueMatchesAny(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)

		if err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("p", "p", 2, ""); err != nil {
			t.Fatalf("removing acme's grants on an empty filter value: %v", err)
		}
		if n := count(t, handle, "authz_grants"); n != 1 {
			t.Errorf("the grant table holds %d row(s), want globex's alone", n)
		}
		if n := count(t, handle, "authz_bindings"); n != 2 {
			t.Errorf("the binding table holds %d row(s), want it untouched", n)
		}
	})
}

// Several filter values name several positions, each one the position before it
// plus one. A removal that read the values at the wrong positions would match a
// different column, or none.
func TestAFilteredRemovalAdvancesThroughSeveralValues(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)

		// Positions 2 and 3 are the resource and the action: "account" and
		// "read" together match acme's grant alone.
		if err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("p", "p", 2, "account", "read"); err != nil {
			t.Fatalf("removing acme's account-read grant: %v", err)
		}
		if n := count(t, handle, "authz_grants"); n != 1 {
			t.Errorf("the grant table holds %d row(s), want globex's alone", n)
		}
	})
}

// A filter over a g rule removes the caller's bindings and no other table's
// rows, and a filter naming another tenant's domain is refused rather than run.
// The two rows the two tenants hold are the same role in their own domain, so
// the removal reaches acme's bindings only where it is held to the tenant and
// reads the g rule's own positions.
func TestAFilteredRemovalOfABindingIsHeldToTheCaller(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)

		// Position 1 of a g rule is the role.
		if err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("g", "g", 1, "roleA"); err != nil {
			t.Fatalf("removing acme's roleA bindings: %v", err)
		}
		if n := count(t, handle, "authz_bindings"); n != 1 {
			t.Errorf("the binding table holds %d row(s), want globex's alone", n)
		}
		// A g removal reaches authz_bindings alone: the grant table is untouched.
		if n := count(t, handle, "authz_grants"); n != 2 {
			t.Errorf("the grant table holds %d row(s), want it untouched", n)
		}

		// Position 2 of a g rule is its domain: naming globex through the acme
		// adapter is refused.
		err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("g", "g", 2, "globex")
		if !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("a g filter naming globex gave %v, want ErrTenantMismatch", err)
		}
		if n := count(t, handle, "authz_bindings"); n != 1 {
			t.Errorf("the refused removal left %d row(s), want the one", n)
		}
	})
}

// A read-only adapter binds the empty identifier, and every removal through it
// is refused: a rule naming a domain is a mismatch, a removal naming no domain
// is refused at the binding, and a filtered removal names no tenant to act on.
// None of them reaches a row.
func TestAReadOnlyAdapterRefusesEveryRemoval(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)
		reader := New(t.Context(), handle, "")

		// A p rule naming a domain is not the (empty) caller's.
		err := reader.RemovePolicy("p", "p", []string{"roleA", "acme", "account", "read"})
		if !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("RemovePolicy naming a domain gave %v, want ErrTenantMismatch", err)
		}
		// A rule naming no domain is refused when the empty identifier reaches
		// the statement.
		err = reader.RemovePolicy("p", "p", []string{"roleA", "", "account", "read"})
		if !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("RemovePolicy naming no domain gave %v, want ErrNoTenantInContext", err)
		}
		// A filtered removal has no tenant to be held to.
		if err := reader.RemoveFilteredPolicy("p", "p", 0, "roleA"); !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("a filtered p removal through a reader gave %v, want ErrTenantMismatch", err)
		}
		if err := reader.RemoveFilteredPolicy("g", "g", 0, "acc1"); !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("a filtered g removal through a reader gave %v, want ErrTenantMismatch", err)
		}

		if n := count(t, handle, "authz_grants"); n != 2 {
			t.Errorf("the grant table holds %d row(s), want both", n)
		}
		if n := count(t, handle, "authz_bindings"); n != 2 {
			t.Errorf("the binding table holds %d row(s), want both", n)
		}
	})
}

// A removal addressed by a rule's whole key reaches the caller's row alone.
// Both tenants hold the same rule, so a removal that dropped the tenant from
// the key would take both rows and the count would show it.
func TestRemovePolicyTouchesOnlyTheCallersIdenticalRow(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)

		for _, id := range []string{"acme", "globex"} {
			if err := handle.WithContext(crossing()).Exec(
				`INSERT INTO authz_grants (tenant_id, role, resource, action, created_at)
				 VALUES (?, 'roleA', 'account', 'read', '2020-01-01T00:00:00Z')`, id).Error; err != nil {
				t.Fatalf("seeding a grant: %v", err)
			}
			if err := handle.WithContext(crossing()).Exec(
				`INSERT INTO authz_bindings (tenant_id, account_id, role, granted_at)
				 VALUES (?, 'acc1', 'roleA', '2020-01-01T00:00:00Z')`, id).Error; err != nil {
				t.Fatalf("seeding a binding: %v", err)
			}
		}

		if err := New(t.Context(), handle, "acme").RemovePolicy("p", "p", []string{"roleA", "acme", "account", "read"}); err != nil {
			t.Fatalf("removing acme's grant: %v", err)
		}
		if n := count(t, handle, "authz_grants"); n != 1 {
			t.Errorf("the grant table holds %d row(s), want globex's identical row alone", n)
		}
		if got := soleTenant(t, handle, "authz_grants"); got != "globex" {
			t.Errorf("the surviving grant belongs to %q, want globex", got)
		}

		if err := New(t.Context(), handle, "acme").RemovePolicy("g", "g", []string{"acc1", "roleA", "acme"}); err != nil {
			t.Fatalf("removing acme's binding: %v", err)
		}
		if n := count(t, handle, "authz_bindings"); n != 1 {
			t.Errorf("the binding table holds %d row(s), want globex's identical row alone", n)
		}
		if got := soleTenant(t, handle, "authz_bindings"); got != "globex" {
			t.Errorf("the surviving binding belongs to %q, want globex", got)
		}
	})
}

// A removal propagates the store's failure rather than swallowing it into a
// success. On a handle whose pool is closed the delete cannot run, and the
// error comes back.
func TestARemovalReportsTheStoresFailure(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)
		pool, err := handle.DB()
		if err != nil {
			t.Fatalf("reaching the handle's pool: %v", err)
		}
		if err = pool.Close(); err != nil {
			t.Fatalf("closing the pool: %v", err)
		}

		err = New(t.Context(), handle, "acme").RemovePolicy("p", "p", []string{"roleA", "acme", "account", "read"})
		if err == nil {
			t.Error("a removal through a closed pool returned no error, want the store's failure")
		}
	})
}

// soleTenant returns the tenant_id of the one row a table holds.
func soleTenant(t *testing.T, handle *gorm.DB, table string) string {
	t.Helper()
	var id string
	if err := handle.Raw("SELECT tenant_id FROM " + table + " LIMIT 1").Scan(&id).Error; err != nil {
		t.Fatalf("reading %s's sole tenant: %v", table, err)
	}
	return id
}
