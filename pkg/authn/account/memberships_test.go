package account

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

// A suspended membership is found but reads as no member, because the zero status is suspended.
func TestAMembershipIsSuspendedResumedAndReadAsAMember(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("joiner", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		if member, err := h.p.IsMember(ctx, id, "acme"); err != nil || !member {
			t.Errorf("an active member reads %v (%v), want a member", member, err)
		}
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: false, Source: "admin"}); err != nil {
			t.Fatalf("suspending: %v", err)
		}
		got, found, err := h.p.Membership(ctx, "acme", id)
		if err != nil || !found || got.Active {
			t.Errorf("a suspended membership reads %+v (found %v, %v), want it found and suspended", got, found, err)
		}
		if member, err := h.p.IsMember(ctx, id, "acme"); err != nil || member {
			t.Errorf("a suspended member reads %v (%v), want no member", member, err)
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 1 {
			t.Errorf("suspending left %d rows, want the one row", n)
		}
	})
}

// A member of no tenant, and a subject of the static table, are no member.
func TestAMemberOfNothingIsNoMember(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("stranger", "")
		if member, err := h.p.IsMember(ctx, id, "acme"); err != nil || member {
			t.Errorf("a stranger reads %v (%v), want no member", member, err)
		}
		if member, err := h.p.IsMember(ctx, authn.AccountID("static.bob"), "acme"); err != nil || member {
			t.Errorf("a static subject reads %v (%v), want no member", member, err)
		}
	})
}

// Put is idempotent: writing the same membership changes nothing and concurrent writes leave one row.
func TestPutIsIdempotent(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("idem", "")
		m := authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}
		const callers = 8
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if err := h.p.Put(ctx, m); err != nil {
					t.Errorf("a concurrent Put returned %v", err)
				}
			}()
		}
		close(start)
		wg.Wait()
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 1 {
			t.Errorf("%d rows for one membership written %d times together, want one", n, callers)
		}
	})
}

// Put on a missing account or a tombstone is ErrAccountNotFound.
func TestPutOnAnAccountThatIsNotThereIsNotFound(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		if err := h.p.Put(ctx, authn.Membership{Account: "nobody-here", Tenant: "acme", Active: true}); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("joining as a stranger gave %v, want ErrAccountNotFound", err)
		}
		id := h.user("gone", "")
		if err := h.p.Delete(ctx, id, authn.ReasonAdmin); err != nil {
			t.Fatalf("deleting: %v", err)
		}
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true}); !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("joining as a tombstone gave %v, want ErrAccountNotFound", err)
		}
	})
}

// A subject of the static table has no membership row and cannot be given one.
func TestPutOnAStaticSubjectIsRefused(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		err := h.p.Put(context.Background(), authn.Membership{Account: "static.bob", Tenant: "acme", Active: true})
		if !errors.Is(err, authn.ErrAccountNotFound) {
			t.Errorf("joining as a static subject gave %v, want ErrAccountNotFound", err)
		}
	})
}

// Members lists a tenant's members a page at a time, by the account identifier.
func TestMembersPagesByAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		for _, name := range []string{"m-one", "m-two", "m-three"} {
			id := h.user(name, "")
			if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}); err != nil {
				t.Fatalf("joining %s: %v", name, err)
			}
		}
		first, err := h.p.Members(ctx, "acme", authn.MemberQuery{Page: authn.Page{Limit: 2}})
		if err != nil || len(first) != 2 {
			t.Fatalf("the first page is %d rows (%v), want two", len(first), err)
		}
		second, err := h.p.Members(ctx, "acme", authn.MemberQuery{Page: authn.Page{After: first[len(first)-1].Account.String(), Limit: 2}})
		if err != nil || len(second) != 1 {
			t.Fatalf("the second page is %d rows (%v), want one", len(second), err)
		}
		if first[0].Account.String() >= first[1].Account.String() {
			t.Error("the first page is not in the order of the account identifiers")
		}
	})
}

// Members narrows a page to accounts whose display name or login identifiers begin with the query.
func TestMembersFiltersByDisplayNameOrIdentifier(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		named := h.user("alpha", "")
		if err := h.p.SetDisplayName(ctx, named, "Ada Lovelace"); err != nil {
			t.Fatalf("setting the display name: %v", err)
		}
		login := h.user("ada-login", "")
		other := h.user("bob", "")
		for _, id := range []authn.AccountID{named, login, other} {
			if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}); err != nil {
				t.Fatalf("joining %s: %v", id, err)
			}
		}

		cases := []struct {
			name  string
			query string
			want  []authn.AccountID
		}{
			{"every member with no query", "", []authn.AccountID{named, login, other}},
			{"a display-name prefix", "Ada L", []authn.AccountID{named}},
			{"an identifier prefix after lower-casing", "ada-log", []authn.AccountID{login}},
			{"a query neither matches", "zzz", nil},
		}
		for _, c := range cases {
			got, err := h.p.Members(ctx, "acme", authn.MemberQuery{Query: c.query})
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			ids := make([]authn.AccountID, len(got))
			for i, m := range got {
				ids[i] = m.Account
			}
			if !sameIDs(ids, c.want) {
				t.Errorf("%s: members = %v, want %v", c.name, ids, c.want)
			}
		}
	})
}

func sameIDs(got, want []authn.AccountID) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[authn.AccountID]bool{}
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			return false
		}
	}
	return true
}

// Every per-tenant statement names the tenant, so nothing reads or writes across tenants by accident.
func TestEveryPerTenantStatementNamesTheTenant(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("tenant-scoped", "")
		rec := h.recordSQL()
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		if _, _, err := h.p.Membership(ctx, "acme", id); err != nil {
			t.Fatalf("reading a membership: %v", err)
		}
		if _, err := h.p.Members(ctx, "acme", authn.MemberQuery{}); err != nil {
			t.Fatalf("listing members: %v", err)
		}
		if err := h.p.Remove(ctx, id, "acme"); err != nil {
			t.Fatalf("removing: %v", err)
		}
		for _, statement := range rec.take() {
			if !strings.Contains(statement, tableMemberships) {
				continue
			}
			if !strings.Contains(statement, "tenant_id") {
				t.Errorf("a membership statement names no tenant: %s", statement)
			}
		}
	})
}

// Removing a membership, present or not, leaves the account without it.
func TestRemoveLeavesNoMembership(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("leaver", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		if err := h.p.Remove(ctx, id, "acme"); err != nil {
			t.Fatalf("removing: %v", err)
		}
		if n := h.count(tableMemberships, "account_id = ?", id.String()); n != 0 {
			t.Errorf("%d row(s) left after removing", n)
		}
		if err := h.p.Remove(ctx, id, "acme"); err != nil {
			t.Errorf("removing a membership that is not there gave %v, want nil", err)
		}
	})
}

// The tenants an account belongs to are listed across tenants, in tenant order.
func TestTenantsListsEveryMembershipOfAnAccount(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("multi", "")
		for _, tenant := range []string{"beta", "alpha"} {
			if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: tenant, Active: true, Source: "admin"}); err != nil {
				t.Fatalf("joining %s: %v", tenant, err)
			}
		}
		got, err := h.p.Tenants(ctx, id)
		if err != nil || len(got) != 2 || got[0].Tenant != "alpha" || got[1].Tenant != "beta" {
			t.Errorf("Tenants gave %+v (%v), want alpha then beta", got, err)
		}
	})
}

// PutJoining writes a membership only into a tenant the account has none in, or over one still active.
func TestPutJoiningNeverResumesASuspendedMembership(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("joiner", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: false, Source: "admin"}); err != nil {
			t.Fatalf("suspending: %v", err)
		}
		joined, err := h.p.PutJoining(ctx, authn.Membership{
			Account: id, Tenant: "acme", Active: true, Source: "invite",
		}, true)
		if err != nil {
			t.Fatalf("joining over a suspended membership: %v", err)
		}
		if joined {
			t.Error("PutJoining resumed a suspended membership")
		}
		got, found, err := h.p.Membership(ctx, "acme", id)
		if err != nil || !found || got.Active || got.Source != "admin" {
			t.Errorf("membership = %+v (found %v, %v), want the suspended admin row left alone", got, found, err)
		}
	})
}

// PutJoining inserts a membership when the account has none and reports it an active member.
func TestPutJoiningInsertsWhenTheAccountHasNoMembership(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("newcomer", "")
		joined, err := h.p.PutJoining(ctx, authn.Membership{
			Account: id, Tenant: "acme", Active: true, Source: "invite",
		}, false)
		if err != nil || !joined {
			t.Fatalf("PutJoining into an empty tenant = %v, %v; want a join", joined, err)
		}
		got, found, err := h.p.Membership(ctx, "acme", id)
		if err != nil || !found || !got.Active || got.Source != "invite" {
			t.Errorf("membership = %+v (found %v, %v), want an active invite row", got, found, err)
		}
	})
}

// PutJoining does not rewrite a membership the caller read active but that has been removed.
func TestPutJoiningRespectsTheRemovalThatFollowedTheRead(t *testing.T) {
	forEachDialect(t, func(t *testing.T, h *harness) {
		ctx := context.Background()
		id := h.user("mover", "")
		if err := h.p.Put(ctx, authn.Membership{Account: id, Tenant: "acme", Active: true, Source: "admin"}); err != nil {
			t.Fatalf("joining: %v", err)
		}
		joined, err := h.p.PutJoining(ctx, authn.Membership{
			Account: id, Tenant: "acme", Active: true, Source: "admin",
		}, true)
		if err != nil || !joined {
			t.Fatalf("PutJoining over an active membership = %v, %v; want a join", joined, err)
		}
		if removeErr := h.p.Remove(ctx, id, "acme"); removeErr != nil {
			t.Fatalf("removing: %v", removeErr)
		}
		joined, err = h.p.PutJoining(ctx, authn.Membership{
			Account: id, Tenant: "acme", Active: true, Source: "invite",
		}, true)
		if err != nil {
			t.Fatalf("joining after the removal: %v", err)
		}
		if joined {
			t.Error("PutJoining wrote a membership again after it had been removed")
		}
		if n := h.count(tableMemberships, "account_id = ? AND tenant_id = ?", id.String(), "acme"); n != 0 {
			t.Errorf("%d row(s) after the lost race, want none", n)
		}
	})
}
