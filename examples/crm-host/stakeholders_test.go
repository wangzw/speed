package main

import (
	nethttp "net/http"
	"slices"
	"strings"
	"testing"
)

// These are the cases for a deal's stakeholders, the one many-to-many
// association the host has: a deal and a contact, both scoped, joined through
// the join model GORM builds, whose tenant column is the one both keys'
// tenant halves are written into.

// stakeholdersOf lists a deal view's stakeholders as identifier and name.
func stakeholdersOf(d dealView) []string {
	out := []string{}
	for _, s := range d.Stakeholders {
		out = append(out, s.ContactID+" "+s.Name)
	}
	return out
}

// linksOf reads, from the join table itself, the contacts a tenant's deal
// names.
func (c *crmCase) linksOf(ref tenantRef, deal string) []string {
	c.t.Helper()
	rows, err := c.sql().Query(
		c.rebind("SELECT contact_id FROM deal_stakeholders WHERE tenant_id = ? AND deal_id = ? ORDER BY contact_id"),
		ref.ID, deal)
	if err != nil {
		c.t.Fatalf("reading %s's stakeholders of %s: %v", ref.ID, deal, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			c.t.Fatalf("scanning a stakeholder row: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatalf("reading %s's stakeholders of %s: %v", ref.ID, deal, err)
	}
	return out
}

// TestADealsStakeholdersStayInItsTenant is the case for the many-to-many
// association between the deals and the contacts, on every stack.
//
// Both tenants hold a deal and three contacts under the same identifiers,
// each contact named after its tenant, so a statement on the join table or a
// read through it that reached the other tenant's rows would say so by a name
// or a count. Each tenant replaces its deal's set, adds to it and reads it back,
// through the deal and through the listing, and the join table holds each
// tenant's links alone: a replacement or an addition in one tenant leaves the
// other's links as they were. A contact the tenant does not hold, or holds
// deleted, is refused, a deal it does not hold is not found, and a change of
// the set moves no version. The last subtest removes one stakeholder through
// the association's deletion, with the contact addressed by its identifier.
//
// The join table has no model of the host's, so every statement on it — the
// replacement's and the addition's inserts and deletions, the preload's read,
// the deletion's — is scoped by the tenant filter from the association alone.
// Written with GORM's default join table, a column per side, every one of
// them is refused, and the case fails at its first deal, whose read back
// preloads the stakeholders.
func TestADealsStakeholdersStayInItsTenant(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.sealWithCaseKey()
		w := startDeals(t, c, nil)
		sides := sidesOf(w)
		for _, side := range sides {
			definePipeline(t, w.h, side.ref, side.cred, pipelineOf(side.name))
			createDealOn(t, w.h, side.ref, side.cred, map[string]any{
				"id": "twin", "account_id": "acc-1", "title": "twin of " + side.name, "amount": 10,
				"currency": "EUR", "stage": "qualify",
			})
			for _, id := range []string{"con-a", "con-b", "con-c"} {
				expect(t, "creating "+id+" in "+side.ref.ID, w.h.send(t, api(nethttp.MethodPost, "/contacts", side.ref,
					side.cred, map[string]any{
						"id": id, "account_id": "acc-1", "name": id + " at " + side.name,
						"email": id + "@" + side.name + ".test",
					})), nethttp.StatusCreated)
			}
		}
		acme, globex := sides[0], sides[1]
		set := func(side salesSide, ids ...string) response {
			return w.h.send(t, api(nethttp.MethodPut, "/deals/twin/stakeholders", side.ref, side.cred,
				map[string]any{"contact_ids": append([]string{}, ids...)}))
		}
		add := func(side salesSide, id string) response {
			return w.h.send(t, api(nethttp.MethodPost, "/deals/twin/stakeholders", side.ref, side.cred,
				map[string]any{"contact_id": id}))
		}
		named := func(side salesSide, ids ...string) []string {
			out := []string{}
			for _, id := range ids {
				out = append(out, id+" at "+side.name)
			}
			return out
		}
		// requireSet holds a tenant's deal to its stakeholders as the deal
		// reads, as the listing reads and as the join table holds them.
		requireSet := func(what string, side salesSide, ids ...string) {
			t.Helper()
			want := named(side, ids...)
			for i := range want {
				want[i] = ids[i] + " " + want[i]
			}
			if got := stakeholdersOf(w.getDeal(t, side.ref, side.cred, "twin")); !slices.Equal(got, want) {
				t.Errorf("%s: %s's twin reads the stakeholders %v, want %v", what, side.ref.ID, got, want)
			}
			resp := w.h.get(t, apiEndpoint, "/deals", side.ref.Alias, side.cred)
			expect(t, "listing "+side.ref.ID+"'s deals", resp, nethttp.StatusOK)
			var list dealList
			resp.decode(t, &list)
			for _, d := range list.Deals {
				if d.ID == "twin" && !slices.Equal(stakeholdersOf(d), want) {
					t.Errorf("%s: %s's listing shows the twin's stakeholders as %v, want %v", what, side.ref.ID,
						stakeholdersOf(d), want)
				}
			}
			if got, linked := c.linksOf(side.ref, "twin"), slices.Sorted(slices.Values(ids)); !slices.Equal(got, linked) {
				t.Errorf("%s: the join table holds %v for %s's twin, want %v", what, got, side.ref.ID, linked)
			}
		}

		resp := set(acme, "con-b", "con-a", "con-b")
		expect(t, "acme naming con-a and con-b", resp, nethttp.StatusOK)
		var answered dealView
		resp.decode(t, &answered)
		if got, want := stakeholdersOf(answered), []string{"con-a con-a at acme", "con-b con-b at acme"}; !slices.Equal(got, want) {
			t.Errorf("acme's replacement answered the stakeholders %v, want %v", got, want)
		}
		if answered.Version != 1 {
			t.Errorf("acme's replacement answered the twin at version %d, want 1: the set is not the deal's row", answered.Version)
		}
		expect(t, "globex naming con-a", set(globex, "con-a"), nethttp.StatusOK)
		requireSet("after both named theirs", acme, "con-a", "con-b")
		requireSet("after both named theirs", globex, "con-a")

		expect(t, "acme replacing its set with con-b and con-c", set(acme, "con-c", "con-b"), nethttp.StatusOK)
		requireSet("after acme's replacement", acme, "con-b", "con-c")
		requireSet("after acme's replacement", globex, "con-a")

		expect(t, "globex adding con-c", add(globex, "con-c"), nethttp.StatusOK)
		expect(t, "globex adding con-c again", add(globex, "con-c"), nethttp.StatusOK)
		requireSet("after globex's additions", globex, "con-a", "con-c")
		requireSet("after globex's additions", acme, "con-b", "con-c")

		// A contact only globex holds is no contact of acme's, and neither is
		// one acme holds under no identifier at all.
		expect(t, "globex opening con-g", w.h.send(t, api(nethttp.MethodPost, "/contacts", globex.ref, globex.cred,
			map[string]any{"id": "con-g", "account_id": "acc-1", "name": "con-g at globex", "email": "con-g@globex.test"})),
			nethttp.StatusCreated)
		for _, refusal := range []struct {
			what string
			resp response
		}{
			{"acme naming globex's con-g", set(acme, "con-b", "con-g")},
			{"acme adding globex's con-g", add(acme, "con-g")},
			{"acme naming con-z, which nobody holds", set(acme, "con-z")},
		} {
			expect(t, refusal.what, refusal.resp, nethttp.StatusUnprocessableEntity)
			if !strings.Contains(string(refusal.resp.body), "con-") {
				t.Errorf("%s answered %s, which names no contact", refusal.what, refusal.resp.body)
			}
		}
		requireSet("after the refused writes", acme, "con-b", "con-c")
		expect(t, "acme naming the stakeholders of a deal it does not hold",
			w.h.send(t, api(nethttp.MethodPut, "/deals/none/stakeholders", acme.ref, acme.cred,
				map[string]any{"contact_ids": []string{"con-b"}})), nethttp.StatusNotFound)
		expect(t, "acme naming no list", w.h.send(t, api(nethttp.MethodPut, "/deals/twin/stakeholders", acme.ref,
			acme.cred, map[string]any{})), nethttp.StatusUnprocessableEntity)

		// A deleted contact is no longer a stakeholder a read shows, and it
		// cannot be named again; its link stays, as the contact's row does.
		expect(t, "acme deleting con-c", w.h.send(t, api(nethttp.MethodDelete, "/contacts/con-c", acme.ref, acme.cred, nil)),
			nethttp.StatusNoContent)
		if got, want := stakeholdersOf(w.getDeal(t, acme.ref, acme.cred, "twin")), []string{"con-b con-b at acme"}; !slices.Equal(got, want) {
			t.Errorf("acme's twin reads the stakeholders %v once con-c was deleted, want %v", got, want)
		}
		expect(t, "acme naming its deleted con-c", set(acme, "con-b", "con-c"), nethttp.StatusUnprocessableEntity)

		expect(t, "acme clearing its set", set(acme), nethttp.StatusOK)
		requireSet("after acme cleared its set", acme)
		requireSet("after acme cleared its set", globex, "con-a", "con-c")

		if got := w.getDeal(t, acme.ref, acme.cred, "twin"); got.Version != 1 {
			t.Errorf("acme's twin is at version %d after its stakeholders changed, want 1", got.Version)
		}

		t.Run("one stakeholder is removed within its tenant", func(t *testing.T) {
			// The association's deletion is given the contact the path names,
			// by the identifier half of its key, as a deal's deletion is given
			// the deal: the tenant half is the filter's to fill. GORM writes
			// the contact's key into the deletion's condition with that half
			// empty, and the filter fills it from the request's tenant, so the
			// deletion takes globex's link and leaves acme's twin's link to
			// the same identifier where it is.
			sub := c.in(t)
			resp := w.h.send(t, api(nethttp.MethodDelete, "/deals/twin/stakeholders/con-a", globex.ref, globex.cred, nil))
			if resp.status != nethttp.StatusNoContent {
				t.Errorf("globex removing con-a from its twin answered %d %s, want 204", resp.status, resp.body)
			}
			if got, want := sub.linksOf(globex.ref, "twin"), []string{"con-c"}; !slices.Equal(got, want) {
				t.Errorf("the join table holds %v for globex's twin after it removed con-a, want %v", got, want)
			}
			expect(t, "acme naming con-a again", set(acme, "con-a"), nethttp.StatusOK)
			resp = w.h.send(t, api(nethttp.MethodDelete, "/deals/twin/stakeholders/con-c", globex.ref, globex.cred, nil))
			if resp.status != nethttp.StatusNoContent {
				t.Errorf("globex removing con-c from its twin answered %d %s, want 204", resp.status, resp.body)
			}
			if got, want := sub.linksOf(acme.ref, "twin"), []string{"con-a"}; !slices.Equal(got, want) {
				t.Errorf("the join table holds %v for acme's twin after globex removed its own, want %v", got, want)
			}
		})
	})
}

// TestAContactADealNamesKeepsItsAccountFromAPurge is the case for the second
// reference a purge meets, on every stack. A deal names its stakeholders among
// its tenant's live contacts, whatever account each is at, so a deal at acc-1
// names a contact at acc-2, which no deal is written at. The purge of acc-2
// removes its contacts first, which the join table's reference to the contacts
// refuses: it is answered as the conflict a deal naming the account is, and
// the account, the contact and the link stay as they were. Once the deal's set
// is cleared, the purge goes through, which shows the link was what refused it.
func TestAContactADealNamesKeepsItsAccountFromAPurge(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.sealWithCaseKey()
		w := startDeals(t, c, nil)
		acme, alice := w.acme, w.asAlice
		definePipeline(t, w.h, acme, alice, pipelineOf("acme"))
		deal := w.createDeal(t, acme, alice, map[string]any{
			"account_id": "acc-1", "title": "naming a contact at acc-2", "amount": 10, "stage": "qualify",
		})
		expect(t, "acme opening con-2 at acc-2", w.h.send(t, api(nethttp.MethodPost, "/contacts", acme, alice,
			map[string]any{"id": "con-2", "account_id": "acc-2", "name": "con-2 at acme", "email": "con-2@acme.test"})),
			nethttp.StatusCreated)
		name := func(ids ...string) response {
			return w.h.send(t, api(nethttp.MethodPut, "/deals/"+deal.ID+"/stakeholders", acme, alice,
				map[string]any{"contact_ids": append([]string{}, ids...)}))
		}
		expect(t, "acme naming con-2 on the deal at acc-1", name("con-2"), nethttp.StatusOK)

		purge := w.h.send(t, api(nethttp.MethodPost, "/accounts/acc-2/purge", acme, alice, nil))
		if purge.status != nethttp.StatusConflict {
			t.Errorf("purging acc-2, one of whose contacts a deal names, answered %d %s, want 409",
				purge.status, purge.body)
		}
		if n := c.count("SELECT count(*) FROM accounts WHERE tenant_id = ? AND id = ?", acme.ID, "acc-2"); n != 1 {
			t.Errorf("acme holds %d rows for acc-2 after the purge a stakeholder refused, want 1", n)
		}
		if n := c.count("SELECT count(*) FROM contacts WHERE tenant_id = ? AND id = ?", acme.ID, "con-2"); n != 1 {
			t.Errorf("acme holds %d rows for con-2 after the purge a stakeholder refused, want 1", n)
		}
		if got, want := c.linksOf(acme, deal.ID), []string{"con-2"}; !slices.Equal(got, want) {
			t.Errorf("the join table holds %v for the deal after the refused purge, want %v", got, want)
		}

		expect(t, "acme clearing the deal's stakeholders", name(), nethttp.StatusOK)
		expect(t, "acme purging acc-2 once no deal names its contact",
			w.h.send(t, api(nethttp.MethodPost, "/accounts/acc-2/purge", acme, alice, nil)), nethttp.StatusNoContent)
		if n := c.count("SELECT count(*) FROM contacts WHERE tenant_id = ? AND id = ?", acme.ID, "con-2"); n != 0 {
			t.Errorf("acme holds %d rows for con-2 after purging acc-2, want none", n)
		}
	})
}
