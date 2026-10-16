package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about the write a rename takes: only the alias
// column changes, the status is read from nowhere, and an alias another tenant
// holds is refused as Put refuses it.

// The one hazard a rename carries is the status it would write back. A snapshot
// holding a tenant active while the table holds it disabled is exactly the
// window the design names, and a rename that took the whole row — Get and then
// Put — writes the stale status and serves a tenant another replica had just
// disabled. Only an alias-only write leaves the table as it is.
func TestSetAliasLeavesAStatusTheSnapshotStillCallsActive(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	// Another replica disables the tenant. This replica's snapshot does not
	// know, and still calls the tenant active.
	if err := r.handle.Exec("UPDATE tenants SET status = 0 WHERE id = 't_acme'").Error; err != nil {
		t.Fatalf("disabling on the table: %v", err)
	}
	if held, _ := rowOf(t, r, "t_acme"); held.Status != int(tenant.StatusDisabled) {
		t.Fatalf("the table holds status %d before the rename, want disabled", held.Status)
	}

	if err := r.SetAlias(context.Background(), "t_acme", "acme-2"); err != nil {
		t.Fatalf("renaming: %v", err)
	}
	held, ok := rowOf(t, r, "t_acme")
	if !ok || held.Alias != "acme-2" || held.Status != int(tenant.StatusDisabled) {
		t.Fatalf("the row is %+v, want alias acme-2 and the status left disabled", held)
	}
}

// The writer sees its own rename at once, and the by-alias lookup follows it:
// the old name is gone and the new one resolves.
func TestSetAliasRenamesAndTheWriterSeesItAtOnce(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	if err := r.SetAlias(context.Background(), "t_acme", "acme-2"); err != nil {
		t.Fatalf("renaming: %v", err)
	}
	got, ok, err := r.ByAlias(context.Background(), "acme-2")
	if err != nil || !ok || got.ID != "t_acme" {
		t.Fatalf("ByAlias(acme-2) = (%+v, %v, %v), want the renamed tenant", got, ok, err)
	}
	if _, ok, _ := r.ByAlias(context.Background(), "acme"); ok {
		t.Fatal("the old alias still resolves")
	}
}

// An alias another tenant holds is refused as Put refuses it, and nothing is
// written.
func TestSetAliasRefusesAnAliasAnotherTenantHolds(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"), insert("t_other", "other"))
	err := r.SetAlias(context.Background(), "t_acme", "other")
	if !errors.Is(err, tenant.ErrAliasConflict) {
		t.Fatalf("renaming onto an alias another tenant holds gave %v, want ErrAliasConflict", err)
	}
	held, _ := rowOf(t, r, "t_acme")
	if held.Alias != "acme" {
		t.Fatalf("the refused rename left the row as %+v, want alias acme", held)
	}
}

// An identifier no row carries is refused, and nothing is written.
func TestSetAliasRefusesAnIdentifierNoRowCarries(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	err := r.SetAlias(context.Background(), "t_ghost", "ghost")
	if !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("renaming an identifier no row carries gave %v, want ErrInvalidTenant", err)
	}
	if countRows(t, r) != 1 {
		t.Fatal("the refused rename wrote a row")
	}
}

// A field outside the identifier's domain is refused before the table is
// touched, so a caller can correct the value and retry.
func TestSetAliasRefusesFieldsOutsideTheDomain(t *testing.T) {
	for name, tc := range map[string]struct {
		id    tenant.ID
		alias string
	}{
		"an empty identifier": {id: "", alias: "acme-2"},
		"a blank identifier":  {id: "t acme", alias: "acme-2"},
		"an empty alias":      {id: "t_acme", alias: ""},
		"a blank alias":       {id: "t_acme", alias: "acme two"},
	} {
		t.Run(name, func(t *testing.T) {
			r := rosterOver(t, insert("t_acme", "acme"))
			if err := r.SetAlias(context.Background(), tc.id, tc.alias); !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("renaming to %+v gave %v, want ErrInvalidTenant", tc, err)
			}
			held, _ := rowOf(t, r, "t_acme")
			if held.Alias != "acme" {
				t.Fatalf("a refused rename left the row as %+v, want alias acme", held)
			}
		})
	}
}
