package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

// Renaming two tenants onto one alias at once both finds it free, and the
// second update meets the column's unique index. That is the same conflict the
// check inside the write reports, and the caller is owed the same answer for
// it: ErrAliasConflict naming the holder, not the driver's constraint
// violation.
func TestAConcurrentSetAliasConflictIsReportedAsOne(t *testing.T) {
	r := rosterOverPostgres(t)
	ctx := context.Background()

	const pairs = 25
	for i := 0; i < pairs; i++ {
		for _, prefix := range []string{"a", "b"} {
			id := fmt.Sprintf("t_%s_%d", prefix, i)
			if err := r.handle.Exec(insert(id, id)).Error; err != nil {
				t.Fatalf("seeding %q: %v", id, err)
			}
		}
	}

	var wg sync.WaitGroup
	errs := make([][2]error, pairs)
	for i := 0; i < pairs; i++ {
		for side, prefix := range []string{"a", "b"} {
			wg.Add(1)
			go func(i, side int, prefix string) {
				defer wg.Done()
				errs[i][side] = r.SetAlias(ctx, tenant.ID(fmt.Sprintf("t_%s_%d", prefix, i)),
					fmt.Sprintf("alias_%d", i))
			}(i, side, prefix)
		}
	}
	wg.Wait()

	var other []error
	for i, pair := range errs {
		refused := 0
		for _, err := range pair {
			switch {
			case err == nil:
			case errors.Is(err, tenant.ErrAliasConflict):
				refused++
			default:
				refused++
				other = append(other, err)
			}
		}
		if refused != 1 {
			t.Fatalf("pair %d renamed onto one alias ended %v, want exactly one refused", i, pair)
		}
	}
	if len(other) > 0 {
		t.Fatalf("%d of %d concurrent conflicts were not reported as ErrAliasConflict; the first: %v",
			len(other), pairs, other[0])
	}
}
