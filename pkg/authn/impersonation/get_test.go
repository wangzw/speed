package impersonation

import (
	"context"
	"errors"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// TestGetReadsOneRecordAndReportsAMissingOne holds the read the administration
// routes use to learn a record's tenant and target before asking the Decider: an
// existing record is returned, and one that is not there is
// authn.ErrNotImpersonating.
func TestGetReadsOneRecordAndReportsAMissingOne(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		got, err := r.p.Get(ctx, g.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Tenant != tenantA || got.Target != targetID {
			t.Errorf("Get returned tenant %q target %q, want %q / %q", got.Tenant, got.Target, tenantA, targetID)
		}
		if _, err := r.p.Get(ctx, "no-such-record"); !errors.Is(err, authn.ErrNotImpersonating) {
			t.Errorf("Get of a missing record: err = %v, want ErrNotImpersonating", err)
		}
	})
}

// TestGetReturnsAnEndedRecord holds that Get is not the validity question: a
// record that has ended is still returned, so a route can read it and revoke it
// idempotently.
func TestGetReturnsAnEndedRecord(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		g, err := r.p.Start(ctx, adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if err = r.p.End(ctx, adminIdentity(t), "done"); err != nil {
			t.Fatalf("End: %v", err)
		}
		got, err := r.p.Get(ctx, g.ID)
		if err != nil {
			t.Fatalf("Get an ended record: %v", err)
		}
		if got.EndedAt.IsZero() {
			t.Errorf("the ended record reads as unended")
		}
	})
}

// TestListConfinesToTheQueriedTenant holds that GrantQuery.Tenant selects in the
// store: a page holds only the queried tenant's records, so a tenant's records
// are not filtered out of a page the other tenants filled.
func TestListConfinesToTheQueriedTenant(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		r.memberships.set(targetID, tenantB, true)
		if _, err := r.p.Start(ctx, adminIdentity(t), request()); err != nil {
			t.Fatalf("Start (tenant A): %v", err)
		}
		if err := r.p.End(ctx, adminIdentity(t), "switch"); err != nil {
			t.Fatalf("End: %v", err)
		}
		if _, err := r.p.Start(ctx, adminIdentity(t), authn.ImpersonationRequest{
			Target: targetID, Tenant: tenantB, Reason: "in B",
		}); err != nil {
			t.Fatalf("Start (tenant B): %v", err)
		}

		page, _, err := r.p.List(ctx, authn.GrantQuery{Tenant: tenantA})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page) != 1 || page[0].Tenant != tenantA {
			t.Errorf("List(Tenant=A) returned %d records, want the one in A", len(page))
		}
	})
}
