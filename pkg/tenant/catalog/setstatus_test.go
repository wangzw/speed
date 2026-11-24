package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

// TestSetStatusRecordsTheOperatorDisableAndClearsItOnEnable checks that an
// operator disable records the operator as the source and enabling clears it.
func TestSetStatusRecordsTheOperatorDisableAndClearsItOnEnable(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	ctx := context.Background()

	if err := r.SetStatus(ctx, "t_acme", tenant.StatusDisabled); err != nil {
		t.Fatalf("disabling: %v", err)
	}
	requireRow(t, r, "t_acme", row{
		ID: "t_acme", Name: "acme",
		Status: int(tenant.StatusDisabled), DisabledBy: int(tenant.DisabledByOperator),
	})

	if err := r.SetStatus(ctx, "t_acme", tenant.StatusActive); err != nil {
		t.Fatalf("enabling: %v", err)
	}
	requireRow(t, r, "t_acme", row{
		ID: "t_acme", Name: "acme",
		Status: int(tenant.StatusActive), DisabledBy: int(tenant.DisabledByOperator),
	})
}

// TestSetStatusRestatesTheOperatorSource checks that an operator disabling an
// already disabled tenant takes the source over from an account.
func TestSetStatusRestatesTheOperatorSource(t *testing.T) {
	for name, source := range map[string]tenant.DisabledBy{
		"an account disable":                tenant.DisabledByAccount,
		"a source this build does not know": tenant.DisabledBy(9),
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDisabled, source))
			if err := r.SetStatus(context.Background(), "t_acme", tenant.StatusDisabled); err != nil {
				t.Fatalf("disabling again: %v", err)
			}
			requireRow(t, r, "t_acme", row{
				ID: "t_acme", Name: "acme",
				Status: int(tenant.StatusDisabled), DisabledBy: int(tenant.DisabledByOperator),
			})
		})
	}
}

// TestSetStatusRefusesTheDeletedStatus checks that SetStatus refuses the
// deleted status before any lookup and writes nothing.
func TestSetStatusRefusesTheDeletedStatus(t *testing.T) {
	for name, seed := range map[string]string{
		"an active row":   insert("t_acme", "acme"),
		"a disabled row":  insertRow("t_acme", "acme", tenant.StatusDisabled, tenant.DisabledByAccount),
		"a tombstone row": insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator),
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOver(t, seed)
			before, _ := rowOf(t, r, "t_acme")
			if err := r.SetStatus(context.Background(), "t_acme", tenant.StatusDeleted); !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("setting the deleted state gave %v, want ErrInvalidTenant", err)
			}
			requireRow(t, r, "t_acme", before)
		})
	}
}

// TestSetStatusRefusesATombstone checks that a status change on a tombstone is
// refused with its own sentinel.
func TestSetStatusRefusesATombstone(t *testing.T) {
	r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
	for _, s := range []tenant.Status{tenant.StatusActive, tenant.StatusDisabled} {
		if err := r.SetStatus(context.Background(), "t_acme", s); !errors.Is(err, tenant.ErrTenantDeleted) {
			t.Fatalf("setting a tombstone to %s gave %v, want ErrTenantDeleted", s, err)
		}
	}
}

// TestSetStatusByAccountDisablesOnlyAnActiveTenant checks that account state
// disables only an active tenant and records the account as the source.
func TestSetStatusByAccountDisablesOnlyAnActiveTenant(t *testing.T) {
	t.Run("an active tenant", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusDisabled); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		requireRow(t, r, "t_acme", row{
			ID: "t_acme", Name: "acme",
			Status: int(tenant.StatusDisabled), DisabledBy: int(tenant.DisabledByAccount),
		})
	})
	t.Run("an already disabled tenant", func(t *testing.T) {
		r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDisabled, tenant.DisabledByOperator))
		reads := countFullReads(t, r)
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusDisabled); err != nil {
			t.Fatalf("disabling an already disabled tenant gave %v, want nil", err)
		}
		requireRow(t, r, "t_acme", row{
			ID: "t_acme", Name: "acme",
			Status: int(tenant.StatusDisabled), DisabledBy: int(tenant.DisabledByOperator),
		})
		if got := reads.Load(); got != 0 {
			t.Errorf("the no-op reread the whole roster %d times, want none", got)
		}
	})
}

// TestSetStatusByAccountOnlyRestoresAnAccountDisable checks that account state
// enables only a tenant it disabled itself.
func TestSetStatusByAccountOnlyRestoresAnAccountDisable(t *testing.T) {
	t.Run("an account disable", func(t *testing.T) {
		r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDisabled, tenant.DisabledByAccount))
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusActive); err != nil {
			t.Fatalf("enabling: %v", err)
		}
		requireRow(t, r, "t_acme", row{
			ID: "t_acme", Name: "acme",
			Status: int(tenant.StatusActive), DisabledBy: int(tenant.DisabledByOperator),
		})
	})
	for name, source := range map[string]tenant.DisabledBy{
		"an operator disable":               tenant.DisabledByOperator,
		"a source this build does not know": tenant.DisabledBy(9),
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDisabled, source))
			reads := countFullReads(t, r)
			if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusActive); err != nil {
				t.Fatalf("enabling gave %v, want nil", err)
			}
			requireRow(t, r, "t_acme", row{
				ID: "t_acme", Name: "acme",
				Status: int(tenant.StatusDisabled), DisabledBy: int(source),
			})
			if got := reads.Load(); got != 0 {
				t.Errorf("the no-op reread the whole roster %d times, want none", got)
			}
		})
	}
}

// TestSetStatusByAccountIgnoresWhatItDoesNotCause checks that transitions
// account state does not cause write nothing and succeed, while an unknown row
// and the deleted state are refused.
func TestSetStatusByAccountIgnoresWhatItDoesNotCause(t *testing.T) {
	t.Run("enabling an active tenant", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusActive); err != nil {
			t.Fatalf("enabling an active tenant gave %v, want nil", err)
		}
		requireRow(t, r, "t_acme", row{ID: "t_acme", Name: "acme", Status: int(tenant.StatusActive)})
	})
	t.Run("disabling twice", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		ctx := context.Background()
		if err := r.SetStatusByAccount(ctx, "t_acme", tenant.StatusDisabled); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		if err := r.SetStatusByAccount(ctx, "t_acme", tenant.StatusDisabled); err != nil {
			t.Fatalf("disabling again gave %v, want nil", err)
		}
		requireRow(t, r, "t_acme", row{
			ID: "t_acme", Name: "acme",
			Status: int(tenant.StatusDisabled), DisabledBy: int(tenant.DisabledByAccount),
		})
	})
	t.Run("an identifier no row carries", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		if err := r.SetStatusByAccount(context.Background(), "t_ghost", tenant.StatusDisabled); !errors.Is(err, tenant.ErrInvalidTenant) {
			t.Fatalf("changing a tenant no row carries gave %v, want ErrInvalidTenant", err)
		}
	})
	t.Run("the deleted state", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusDeleted); !errors.Is(err, tenant.ErrInvalidTenant) {
			t.Fatalf("setting the deleted state gave %v, want ErrInvalidTenant", err)
		}
		requireRow(t, r, "t_acme", row{ID: "t_acme", Name: "acme", Status: int(tenant.StatusActive)})
	})
	t.Run("a tombstone", func(t *testing.T) {
		r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusActive); !errors.Is(err, tenant.ErrTenantDeleted) {
			t.Fatalf("enabling a tombstone gave %v, want ErrTenantDeleted", err)
		}
	})
}

// TestPutAndCreateRefuseWhatOnlyAnotherRouteWrites checks that Put and Create
// refuse an account-caused disable and the deleted state before writing
// anything.
func TestPutAndCreateRefuseWhatOnlyAnotherRouteWrites(t *testing.T) {
	for name, write := range map[string]func(*roster) error{
		"Put an account disable": func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{
				ID: "t_new", Name: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledByAccount,
			})
		},
		"Create an account disable": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{
				ID: "t_new", Name: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledByAccount,
			})
		},
		"Put a source this build does not know": func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{
				ID: "t_new", Name: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledBy(9),
			})
		},
		"Create a source this build does not know": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{
				ID: "t_new", Name: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledBy(9),
			})
		},
		"Put the deleted state": func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusDeleted})
		},
		"Create the deleted state": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{ID: "t_new", Name: "new", Status: tenant.StatusDeleted})
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOver(t)
			if err := write(r); !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("%s gave %v, want ErrInvalidTenant", name, err)
			}
			if got := countRows(t, r); got != 0 {
				t.Errorf("the refused write left %d rows, want none", got)
			}
		})
	}
}

// TestTheSnapshotNormalisesTheDisableSource checks that only the account's
// source value reads as the account's and an unknown value reads as the
// operator's.
func TestTheSnapshotNormalisesTheDisableSource(t *testing.T) {
	r := rosterOver(t,
		insertRow("t_account", "account", tenant.StatusDisabled, tenant.DisabledByAccount),
		insertRow("t_unknown", "unknown", tenant.StatusDisabled, tenant.DisabledBy(9)),
		insertRow("t_operator", "operator", tenant.StatusDisabled, tenant.DisabledByOperator),
	)
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading the roster: %v", err)
	}
	for id, want := range map[tenant.ID]tenant.DisabledBy{
		"t_account":  tenant.DisabledByAccount,
		"t_unknown":  tenant.DisabledByOperator,
		"t_operator": tenant.DisabledByOperator,
	} {
		got, ok, err := r.Get(ctx, id)
		if err != nil || !ok {
			t.Fatalf("reading %q: found=%v err=%v", id, ok, err)
		}
		if got.DisabledBy != want {
			t.Errorf("the snapshot reads %q's source as %s, want %s", id, got.DisabledBy, want)
		}
	}
}

// TestARefusedValueIsNamedByItsNumber checks that a refusal names the
// underlying number of the refused status or source, not its display spelling.
func TestARefusedValueIsNamedByItsNumber(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	ctx := context.Background()

	for name, refused := range map[string]error{
		"SetStatus":          r.SetStatus(ctx, "t_acme", tenant.Status(7)),
		"SetStatusByAccount": r.SetStatusByAccount(ctx, "t_acme", tenant.Status(7)),
	} {
		if !errors.Is(refused, tenant.ErrInvalidTenant) {
			t.Fatalf("%s of an unknown status gave %v, want ErrInvalidTenant", name, refused)
		}
		if !strings.Contains(refused.Error(), "7") {
			t.Errorf("%s does not name the refused status 7: %v", name, refused)
		}
		if strings.Contains(refused.Error(), "disabled is not a status") {
			t.Errorf("%s names the display spelling of the refused status instead of its number: %v",
				name, refused)
		}
	}

	for name, write := range map[string]func() error{
		"Put": func() error {
			return r.Put(ctx, tenant.Tenant{
				ID: "t_new", Name: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledBy(9),
			})
		},
		"Create": func() error {
			return r.Create(ctx, tenant.Tenant{
				ID: "t_new", Name: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledBy(9),
			})
		},
	} {
		err := write()
		if !errors.Is(err, tenant.ErrInvalidTenant) {
			t.Fatalf("%s of an unknown disable source gave %v, want ErrInvalidTenant", name, err)
		}
		if !strings.Contains(err.Error(), "9") {
			t.Errorf("%s does not name the refused disable source 9: %v", name, err)
		}
		if strings.Contains(err.Error(), "source operator") {
			t.Errorf("%s names the display spelling of the refused source instead of its number: %v",
				name, err)
		}
	}
}
