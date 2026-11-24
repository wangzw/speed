package persist

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/tenant"
)

func clear(t *testing.T, handle *gorm.DB) {
	t.Helper()
	for _, table := range []string{"authz_grants", "authz_bindings"} {
		if err := handle.WithContext(crossing()).Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("clearing %s: %v", table, err)
		}
	}
}

// TestAFilteredRemovalReadsEveryFieldPosition checks that a filtered removal reads every field position.
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
				if n := count(t, handle, "authz_grants"); n != 1 {
					t.Errorf("the removal left %d row(s), want the other tenant's alone", n)
				}
			})
		}
	})
}

// TestAFilteredRemovalWithAnEmptyValueMatchesAny checks that a filtered removal with an empty value matches any.
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

// TestAFilteredRemovalAdvancesThroughSeveralValues checks that a filtered removal advances through several values.
func TestAFilteredRemovalAdvancesThroughSeveralValues(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)

		if err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("p", "p", 2, "account", "read"); err != nil {
			t.Fatalf("removing acme's account-read grant: %v", err)
		}
		if n := count(t, handle, "authz_grants"); n != 1 {
			t.Errorf("the grant table holds %d row(s), want globex's alone", n)
		}
	})
}

// TestAFilteredRemovalOfABindingIsHeldToTheCaller checks that a filtered removal of a binding is held to the caller.
func TestAFilteredRemovalOfABindingIsHeldToTheCaller(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)

		if err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("g", "g", 1, "roleA"); err != nil {
			t.Fatalf("removing acme's roleA bindings: %v", err)
		}
		if n := count(t, handle, "authz_bindings"); n != 1 {
			t.Errorf("the binding table holds %d row(s), want globex's alone", n)
		}
		if n := count(t, handle, "authz_grants"); n != 2 {
			t.Errorf("the grant table holds %d row(s), want it untouched", n)
		}

		err := New(t.Context(), handle, "acme").RemoveFilteredPolicy("g", "g", 2, "globex")
		if !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("a g filter naming globex gave %v, want ErrTenantMismatch", err)
		}
		if n := count(t, handle, "authz_bindings"); n != 1 {
			t.Errorf("the refused removal left %d row(s), want the one", n)
		}
	})
}

// TestAReadOnlyAdapterRefusesEveryRemoval checks that a read only adapter refuses every removal.
func TestAReadOnlyAdapterRefusesEveryRemoval(t *testing.T) {
	onEachDialect(t, func(t *testing.T, d dialect) {
		handle := open(t, d, false)
		seed(t, handle)
		reader := New(t.Context(), handle, "")

		err := reader.RemovePolicy("p", "p", []string{"roleA", "acme", "account", "read"})
		if !errors.Is(err, authz.ErrTenantMismatch) {
			t.Errorf("RemovePolicy naming a domain gave %v, want ErrTenantMismatch", err)
		}
		err = reader.RemovePolicy("p", "p", []string{"roleA", "", "account", "read"})
		if !errors.Is(err, tenant.ErrNoTenantInContext) {
			t.Errorf("RemovePolicy naming no domain gave %v, want ErrNoTenantInContext", err)
		}
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

// TestRemovePolicyTouchesOnlyTheCallersIdenticalRow checks that remove policy touches only the callers identical row.
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

// TestARemovalReportsTheStoresFailure checks that a removal reports the stores failure.
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

func soleTenant(t *testing.T, handle *gorm.DB, table string) string {
	t.Helper()
	var id string
	if err := handle.Raw("SELECT tenant_id FROM " + table + " LIMIT 1").Scan(&id).Error; err != nil {
		t.Fatalf("reading %s's sole tenant: %v", table, err)
	}
	return id
}
