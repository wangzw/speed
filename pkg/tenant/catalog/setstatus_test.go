package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about the two status transitions and the disable
// source each records: the operator's path, account state's path, and which
// calls are allowed to write at all.

// TestSetStatusRecordsTheOperatorDisableAndClearsItOnEnable holds the operator
// path whole: disabling writes the operator as the source, and enabling clears
// the source, so a later account recovery has nothing to revoke.
func TestSetStatusRecordsTheOperatorDisableAndClearsItOnEnable(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	ctx := context.Background()

	if err := r.SetStatus(ctx, "t_acme", tenant.StatusDisabled); err != nil {
		t.Fatalf("disabling: %v", err)
	}
	requireRow(t, r, "t_acme", row{
		ID: "t_acme", Alias: "acme",
		Status: int(tenant.StatusDisabled), DisabledBy: int(tenant.DisabledByOperator),
	})

	if err := r.SetStatus(ctx, "t_acme", tenant.StatusActive); err != nil {
		t.Fatalf("enabling: %v", err)
	}
	requireRow(t, r, "t_acme", row{
		ID: "t_acme", Alias: "acme",
		Status: int(tenant.StatusActive), DisabledBy: int(tenant.DisabledByOperator),
	})
}

// The operator disabling a tenant that is already disabled restates the
// decision, and the source becomes the operator's: a row an account disabled
// must not stay revocable by that account after an operator has taken it over.
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
				ID: "t_acme", Alias: "acme",
				Status: int(tenant.StatusDisabled), DisabledBy: int(tenant.DisabledByOperator),
			})
		})
	}
}

// The deleted state is not a status write's to set: deletion has one route,
// Delete. The refusal comes before any lookup, so it writes nothing whatever
// the row holds.
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

// A status change on a tombstone is refused with its own sentinel, so a caller
// that waits out a disabled tenant is told this one is not coming back.
func TestSetStatusRefusesATombstone(t *testing.T) {
	r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
	for _, s := range []tenant.Status{tenant.StatusActive, tenant.StatusDisabled} {
		if err := r.SetStatus(context.Background(), "t_acme", s); !errors.Is(err, tenant.ErrTenantDeleted) {
			t.Fatalf("setting a tombstone to %s gave %v, want ErrTenantDeleted", s, err)
		}
	}
}

// Account state disabling a tenant writes the account as the source, and only
// from active: a tenant already disabled — by anyone — is not account state's to
// disable again.
func TestSetStatusByAccountDisablesOnlyAnActiveTenant(t *testing.T) {
	t.Run("an active tenant", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusDisabled); err != nil {
			t.Fatalf("disabling: %v", err)
		}
		requireRow(t, r, "t_acme", row{
			ID: "t_acme", Alias: "acme",
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
			ID: "t_acme", Alias: "acme",
			Status: int(tenant.StatusDisabled), DisabledBy: int(tenant.DisabledByOperator),
		})
		if got := reads.Load(); got != 0 {
			t.Errorf("the no-op reread the whole roster %d times, want none", got)
		}
	})
}

// Account state enables only a tenant it disabled itself. An operator's disable
// is not account state's to revoke, which is what keeps a tenant an operator
// took out of service from coming back when its account does.
func TestSetStatusByAccountOnlyRestoresAnAccountDisable(t *testing.T) {
	t.Run("an account disable", func(t *testing.T) {
		r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDisabled, tenant.DisabledByAccount))
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusActive); err != nil {
			t.Fatalf("enabling: %v", err)
		}
		requireRow(t, r, "t_acme", row{
			ID: "t_acme", Alias: "acme",
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
				ID: "t_acme", Alias: "acme",
				Status: int(tenant.StatusDisabled), DisabledBy: int(source),
			})
			if got := reads.Load(); got != 0 {
				t.Errorf("the no-op reread the whole roster %d times, want none", got)
			}
		})
	}
}

// Every transition account state's path does not cause writes nothing and
// succeeds, so a repeated event changes nothing rather than failing. What it
// cannot place is refused: no row and the deleted state are answers.
func TestSetStatusByAccountIgnoresWhatItDoesNotCause(t *testing.T) {
	t.Run("enabling an active tenant", func(t *testing.T) {
		r := rosterOver(t, insert("t_acme", "acme"))
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusActive); err != nil {
			t.Fatalf("enabling an active tenant gave %v, want nil", err)
		}
		requireRow(t, r, "t_acme", row{ID: "t_acme", Alias: "acme", Status: int(tenant.StatusActive)})
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
			ID: "t_acme", Alias: "acme",
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
		requireRow(t, r, "t_acme", row{ID: "t_acme", Alias: "acme", Status: int(tenant.StatusActive)})
	})
	t.Run("a tombstone", func(t *testing.T) {
		r := rosterOver(t, insertRow("t_acme", "acme", tenant.StatusDeleted, tenant.DisabledByOperator))
		if err := r.SetStatusByAccount(context.Background(), "t_acme", tenant.StatusActive); !errors.Is(err, tenant.ErrTenantDeleted) {
			t.Fatalf("enabling a tombstone gave %v, want ErrTenantDeleted", err)
		}
	})
}

// A registration cannot write an account-caused disable, nor the deleted state:
// only the two status transitions may write a source, and only Delete reaches
// deletion. All four calls are refused before anything is written.
func TestPutAndCreateRefuseWhatOnlyAnotherRouteWrites(t *testing.T) {
	for name, write := range map[string]func(*roster) error{
		"Put an account disable": func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{
				ID: "t_new", Alias: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledByAccount,
			})
		},
		"Create an account disable": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{
				ID: "t_new", Alias: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledByAccount,
			})
		},
		"Put a source this build does not know": func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{
				ID: "t_new", Alias: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledBy(9),
			})
		},
		"Create a source this build does not know": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{
				ID: "t_new", Alias: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledBy(9),
			})
		},
		"Put the deleted state": func(r *roster) error {
			return r.Put(context.Background(), tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusDeleted})
		},
		"Create the deleted state": func(r *roster) error {
			return r.Create(context.Background(), tenant.Tenant{ID: "t_new", Alias: "new", Status: tenant.StatusDeleted})
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

// The snapshot normalises the source column on the way in: only the account's
// value reads as the account's, and a value this build does not know reads as
// the operator's, so an unexplained disable is never revoked by account state.
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

// A refused value is named by the value that was refused, not by the name the
// display spelling happens to give it: a status this build does not know reads
// as disabled and a source it does not know reads as operator, so a message that
// reused those spellings would name a value that is accepted. Each refusal below
// must carry the underlying number and must not carry the display spelling.
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
				ID: "t_new", Alias: "new",
				Status: tenant.StatusDisabled, DisabledBy: tenant.DisabledBy(9),
			})
		},
		"Create": func() error {
			return r.Create(ctx, tenant.Tenant{
				ID: "t_new", Alias: "new",
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
