package main

import (
	"context"
	nethttp "net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	authzguard "github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// The cases of the accounts and contacts slice. Each claim under "Accounts and
// contacts" in main.go's package comment has its case here or in
// contacts_test.go.
//
// Every statement form the two modules issue on a scoped table is run in two
// tenants that hold records under the same identifiers, which the tenant-led
// keys allow. That is what makes a missing tenant condition visible: a read
// without one finds the other tenant's row under the identifier asked for, and
// a write without one changes it.

// caseRootKey is the root key the cases configure for the database module's
// field encryption. The contacts table seals two columns, and an assembly
// without a key starts and then fails every statement that reaches one.
const caseRootKey = "crm-host case root key, 32 bytes"

// sealWithCaseKey configures the case's root key on the stack's engine. The
// engine's configuration namespace is its module name.
func (c *crmCase) sealWithCaseKey() {
	c.config.set(c.stack.engine+".encryption-key", caseRootKey)
}

// crew is who a slice case acts as. Alice owns both tenants, Ivan, Rita and
// Vera hold one role each in acme alone, and Gus is a rep of globex alone.
type crew struct {
	acme, globex                 tenantRef
	alice, ivan, rita, vera, gus userRef
	tokens                       map[string]string
}

// setUpCrew registers the two tenants and the five users through the admin
// endpoint, grants the roles and signs each user in once. Every request after
// that carries a session cookie, so the password derivation is paid once per
// user rather than once per request.
func setUpCrew(t *testing.T, c *crmCase, h *crmHost) crew {
	t.Helper()
	cr := crew{acme: c.tenantNamed("acme"), globex: c.tenantNamed("globex"), tokens: map[string]string{}}
	h.adminCreateTenant(t, cr.acme)
	h.adminCreateTenant(t, cr.globex)
	cr.alice = h.adminCreateUser(t, c.userNamed("alice"))
	cr.ivan = h.adminCreateUser(t, c.userNamed("ivan"))
	cr.rita = h.adminCreateUser(t, c.userNamed("rita"))
	cr.vera = h.adminCreateUser(t, c.userNamed("vera"))
	cr.gus = h.adminCreateUser(t, c.userNamed("gus"))
	h.adminGrant(t, cr.acme, cr.alice, roleOwner)
	h.adminGrant(t, cr.globex, cr.alice, roleOwner)
	h.adminGrant(t, cr.acme, cr.ivan, roleAdmin)
	h.adminGrant(t, cr.acme, cr.rita, roleRep)
	h.adminGrant(t, cr.acme, cr.vera, roleViewer)
	h.adminGrant(t, cr.globex, cr.gus, roleRep)
	for _, u := range []userRef{cr.alice, cr.ivan, cr.rita, cr.vera, cr.gus} {
		cr.tokens[u.ID] = h.login(t, u)
	}
	return cr
}

// actor is one user acting in one tenant on the API endpoint.
type actor struct {
	h      *crmHost
	user   userRef
	tenant tenantRef
	token  string
}

// as is a crew member acting in a tenant.
func (cr crew) as(h *crmHost, u userRef, in tenantRef) actor {
	return actor{h: h, user: u, tenant: in, token: cr.tokens[u.ID]}
}

// sessionActor is how the journal names an actor who authenticated with a
// session cookie: by the account, which is the account identifier.
func (a actor) sessionActor() string { return a.user.ID }

// send makes one request as the actor.
func (a actor) send(t *testing.T, method, path string, body any) response {
	t.Helper()
	return a.h.send(t, request{
		method: method, endpoint: apiEndpoint, path: path, tenant: a.tenant.Alias, cred: bearer(a.token), body: body,
	})
}

// must makes one request as the actor and fails the case unless it is answered
// with the status given.
func (a actor) must(t *testing.T, method, path string, body any, status int) response {
	t.Helper()
	resp := a.send(t, method, path, body)
	expect(t, a.user.Email+" in "+a.tenant.ID+": "+method+" "+path, resp, status)
	return resp
}

// refused holds a response to a 403 that carries a diagnostic. A 403 comes from
// two writers with two bodies: a handler of this host writes the host's own
// error body (`{"error": ...}`), and the authorization layer's middleware writes
// an RFC 9457 problem detail (`{"type", "status", "detail", ...}`). The case
// asserts only that the refusal carries some text, so a role that is refused for
// want of a permission passes whether the route's handler or the policy refused
// it.
func refused(t *testing.T, what string, resp response) {
	t.Helper()
	expect(t, what, resp, nethttp.StatusForbidden)
	var body struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
		Title  string `json:"title"`
	}
	resp.decode(t, &body)
	if body.Error == "" && body.Detail == "" && body.Title == "" {
		t.Errorf("%s was refused with no diagnostic text: %q", what, resp.body)
	}
}

// storedAccount is an account row as the table holds it.
type storedAccount struct {
	name, domain, industry, owner string
	deleted                       bool
}

// accountRows reads every row of the accounts table under one identifier, soft
// deleted or not, keyed by tenant.
func (c *crmCase) accountRows(id string) map[string]storedAccount {
	c.t.Helper()
	rows, err := c.sql().Query(c.rebind(
		"SELECT tenant_id, name, domain, industry, owner, (deleted_at IS NOT NULL) FROM accounts WHERE id = ?"), id)
	if err != nil {
		c.t.Fatalf("reading the accounts under %s: %v", id, err)
	}
	defer rows.Close()
	out := map[string]storedAccount{}
	for rows.Next() {
		var tenantID string
		var a storedAccount
		if err := rows.Scan(&tenantID, &a.name, &a.domain, &a.industry, &a.owner, &a.deleted); err != nil {
			c.t.Fatalf("scanning an account row: %v", err)
		}
		out[tenantID] = a
	}
	if err := rows.Err(); err != nil {
		c.t.Fatalf("reading the accounts under %s: %v", id, err)
	}
	return out
}

// accountIDs lists the identifiers of a page, in order.
func accountIDs(page accountPage) []string {
	var ids []string
	for _, a := range page.Accounts {
		ids = append(ids, a.ID)
	}
	return ids
}

// TestAccountsStayInTheirTenant is the case for every statement form the
// accounts module issues, each in two tenants holding the same identifiers, and
// for the role each route takes.
func TestAccountsStayInTheirTenant(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.sealWithCaseKey()
		c.migrate()
		// The page size is two for every tenant but globex, whose own row
		// says three: the tenant's layer is read, and it falls back to the
		// global row.
		c.setGlobal(pageSizeSetting, "2")
		c.exec("INSERT INTO settings (tenant_id, key, value, version, updated_at) VALUES (?, ?, ?, 1, ?)",
			c.tenantNamed("globex").ID, pageSizeSetting, "3", time.Now().UTC())
		h := c.serve()
		cr := setUpCrew(t, c, h)
		aliceAcme, aliceGlobex := cr.as(h, cr.alice, cr.acme), cr.as(h, cr.alice, cr.globex)
		ivan, rita, vera := cr.as(h, cr.ivan, cr.acme), cr.as(h, cr.rita, cr.acme), cr.as(h, cr.vera, cr.acme)
		gus := cr.as(h, cr.gus, cr.globex)

		t.Run("one identifier in two tenants is two accounts", func(t *testing.T) {
			sub := c.in(t)
			var made accountView
			aliceAcme.must(t, nethttp.MethodPost, "/accounts", map[string]any{
				"id": "acc-1", "name": "Rocket Works", "domain": "rocket.test", "industry": "aerospace",
			}, nethttp.StatusCreated).decode(t, &made)
			if made.TenantID != cr.acme.ID || made.ID != "acc-1" || made.Owner != cr.alice.ID {
				t.Errorf("the creation in acme answered %+v, want acc-1 in %s owned by %s", made, cr.acme.ID, cr.alice.ID)
			}
			aliceGlobex.must(t, nethttp.MethodPost, "/accounts", map[string]any{
				"id": "acc-1", "name": "Rocket Supply", "domain": "supply.test", "industry": "aerospace",
			}, nethttp.StatusCreated).decode(t, &made)
			if made.TenantID != cr.globex.ID {
				t.Errorf("the creation in globex answered %+v, want it in %s", made, cr.globex.ID)
			}

			// The rows name no tenant when they are written, and the table
			// holds each under the tenant its request named.
			stored := sub.accountRows("acc-1")
			if len(stored) != 2 || stored[cr.acme.ID].name != "Rocket Works" || stored[cr.globex.ID].name != "Rocket Supply" {
				t.Errorf("the table holds acc-1 as %+v, want Rocket Works in %s and Rocket Supply in %s",
					stored, cr.acme.ID, cr.globex.ID)
			}

			for _, tc := range []struct {
				who  actor
				want string
			}{{aliceAcme, "Rocket Works"}, {aliceGlobex, "Rocket Supply"}} {
				var got accountDetail
				tc.who.must(t, nethttp.MethodGet, "/accounts/acc-1", nil, nethttp.StatusOK).decode(t, &got)
				if got.Name != tc.want || got.TenantID != tc.who.tenant.ID {
					t.Errorf("acc-1 read in %s is %+v, want %s of that tenant", tc.who.tenant.ID, got.accountView, tc.want)
				}
			}
		})

		t.Run("a second account under one identifier in one tenant is refused as a conflict", func(t *testing.T) {
			sub := c.in(t)
			resp := aliceAcme.send(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-1", "name": "Rocket Clone"})
			// The primary key refuses the row whatever the answer says.
			if stored := sub.accountRows("acc-1"); len(stored) != 2 || stored[cr.acme.ID].name != "Rocket Works" {
				t.Errorf("the table holds acc-1 as %+v after a second creation in acme, want Rocket Works kept", stored)
			}
			// The refusal is a conflict: the host tells it from any other
			// failure by the database module's mark for a key violation,
			// gorm.ErrDuplicatedKey, the same on both dialects.
			if resp.status != nethttp.StatusConflict {
				t.Errorf("a second acc-1 in acme answered %d %s, want %d", resp.status, resp.body, nethttp.StatusConflict)
			}
		})

		t.Run("the listing pages within the tenant by the tenant's page size", func(t *testing.T) {
			// The two Initrodes are written against the order of their
			// identifiers, so a listing that left the tie to the order the
			// rows were stored in would answer them the other way round.
			for _, body := range []map[string]any{
				{"id": "acc-3", "name": "Initrode"},
				{"id": "acc-2", "name": "Initrode"},
				{"id": "acc-4", "name": "Ahoy! Freight"},
			} {
				aliceAcme.must(t, nethttp.MethodPost, "/accounts", body, nethttp.StatusCreated)
			}
			for _, body := range []map[string]any{
				{"id": "acc-2", "name": "Initrode"},
				{"id": "acc-5", "name": "Zeta Logistics"},
			} {
				aliceGlobex.must(t, nethttp.MethodPost, "/accounts", body, nethttp.StatusCreated)
			}

			// Ordered by name and then identifier, so the two Initrodes
			// always come in one order and a page boundary between them is
			// the same on every read.
			for _, tc := range []struct {
				who   actor
				query string
				size  int
				total int64
				ids   []string
			}{
				{aliceAcme, "", 2, 4, []string{"acc-4", "acc-2"}},
				{aliceAcme, "?page=2", 2, 4, []string{"acc-3", "acc-1"}},
				{aliceAcme, "?page=3", 2, 4, nil},
				{aliceGlobex, "", 3, 3, []string{"acc-2", "acc-1", "acc-5"}},
				{aliceAcme, "?q=ROCK", 2, 1, []string{"acc-1"}},
				{aliceGlobex, "?q=rock", 3, 1, []string{"acc-1"}},
				// A wildcard in the search is a character to find, not a
				// wildcard: no name holds a percent sign, and none holds
				// "r_cket", which the single-character wildcard would read
				// as matching Rocket Works.
				{aliceAcme, "?q=%25", 2, 0, nil},
				{aliceAcme, "?q=r_cket", 2, 0, nil},
				// Nor is the escape character the pattern declares: an
				// exclamation mark finds the one name holding it.
				{aliceAcme, "?q=%21", 2, 1, []string{"acc-4"}},
			} {
				var page accountPage
				tc.who.must(t, nethttp.MethodGet, "/accounts"+tc.query, nil, nethttp.StatusOK).decode(t, &page)
				if page.PageSize != tc.size || page.Total != tc.total || !slices.Equal(accountIDs(page), tc.ids) {
					t.Errorf("GET /accounts%s in %s answered size %d total %d %v, want size %d total %d %v",
						tc.query, tc.who.tenant.ID, page.PageSize, page.Total, accountIDs(page), tc.size, tc.total, tc.ids)
				}
				for _, a := range page.Accounts {
					if a.TenantID != tc.who.tenant.ID {
						t.Errorf("GET /accounts%s in %s answered %s of %s", tc.query, tc.who.tenant.ID, a.ID, a.TenantID)
					}
				}
			}
			expect(t, "a page that is not a whole number from 1",
				aliceAcme.send(t, nethttp.MethodGet, "/accounts?page=0", nil), nethttp.StatusUnprocessableEntity)
		})

		t.Run("a partial update through the model writes the named columns in the tenant", func(t *testing.T) {
			sub := c.in(t)
			var got accountView
			aliceAcme.must(t, nethttp.MethodPatch, "/accounts/acc-1", map[string]any{"industry": "launch services"},
				nethttp.StatusOK).decode(t, &got)
			if got.Name != "Rocket Works" || got.Domain != "rocket.test" || got.Industry != "launch services" {
				t.Errorf("the update answered %+v, want only the industry changed", got)
			}
			if !got.UpdatedAt.After(got.CreatedAt) {
				t.Errorf("the update left updated_at at %s, created %s", got.UpdatedAt, got.CreatedAt)
			}
			// A member given as empty is written as empty: the columns the
			// body names are selected, so a zero value is not skipped.
			aliceAcme.must(t, nethttp.MethodPatch, "/accounts/acc-1", map[string]any{"domain": ""},
				nethttp.StatusOK).decode(t, &got)
			if got.Domain != "" || got.Industry != "launch services" {
				t.Errorf("emptying the domain answered %+v", got)
			}
			if other := sub.accountRows("acc-1")[cr.globex.ID]; other.industry != "aerospace" || other.domain != "supply.test" {
				t.Errorf("globex's acc-1 is %+v after acme's updates, want it untouched", other)
			}
			// An identifier only the other tenant holds is not found, and the
			// other tenant's row is not written.
			expect(t, "an update in acme of globex's acc-5",
				aliceAcme.send(t, nethttp.MethodPatch, "/accounts/acc-5", map[string]any{"name": "Taken Over"}),
				nethttp.StatusNotFound)
			if other := sub.accountRows("acc-5")[cr.globex.ID]; other.name != "Zeta Logistics" {
				t.Errorf("globex's acc-5 is %+v after acme's update of that identifier", other)
			}
			expect(t, "an update naming nothing",
				aliceAcme.send(t, nethttp.MethodPatch, "/accounts/acc-1", map[string]any{}), nethttp.StatusUnprocessableEntity)
			expect(t, "an update emptying the name",
				aliceAcme.send(t, nethttp.MethodPatch, "/accounts/acc-1", map[string]any{"name": " "}),
				nethttp.StatusUnprocessableEntity)
		})

		t.Run("an ownership change writes one column in the tenant and takes a member of it", func(t *testing.T) {
			sub := c.in(t)
			var got accountView
			aliceAcme.must(t, nethttp.MethodPost, "/accounts/acc-1/owner", map[string]any{"owner": cr.rita.ID},
				nethttp.StatusOK).decode(t, &got)
			if got.Owner != cr.rita.ID || got.Industry != "launch services" {
				t.Errorf("the ownership change answered %+v, want rita as owner and nothing else changed", got)
			}
			if other := sub.accountRows("acc-1")[cr.globex.ID]; other.owner != cr.alice.ID {
				t.Errorf("globex's acc-1 is owned by %s after acme's change, want %s", other.owner, cr.alice.ID)
			}
			// Gus is a member of globex alone, and the membership read is
			// scoped to the tenant the request names.
			expect(t, "handing acme's acc-1 to a member of globex",
				aliceAcme.send(t, nethttp.MethodPost, "/accounts/acc-1/owner", map[string]any{"owner": cr.gus.ID}),
				nethttp.StatusUnprocessableEntity)
			expect(t, "handing globex's acc-1 to gus",
				aliceGlobex.send(t, nethttp.MethodPost, "/accounts/acc-1/owner", map[string]any{"owner": cr.gus.ID}),
				nethttp.StatusOK)
			if mine := sub.accountRows("acc-1")[cr.acme.ID]; mine.owner != cr.rita.ID {
				t.Errorf("acme's acc-1 is owned by %s after globex's change, want %s", mine.owner, cr.rita.ID)
			}
		})

		t.Run("the detail preloads the account's own contacts", func(t *testing.T) {
			rita.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-1", "account_id": "acc-1", "name": "Ada Lovelace", "email": "ada@example.test",
			}, nethttp.StatusCreated)
			rita.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-2", "account_id": "acc-2", "name": "Bea Brown", "email": "bea@example.test",
			}, nethttp.StatusCreated)
			gus.must(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-1", "account_id": "acc-1", "name": "Grace Hopper", "email": "grace@example.test",
			}, nethttp.StatusCreated)

			for _, tc := range []struct {
				who  actor
				want string
			}{{aliceAcme, "Ada Lovelace"}, {aliceGlobex, "Grace Hopper"}} {
				var got accountDetail
				tc.who.must(t, nethttp.MethodGet, "/accounts/acc-1", nil, nethttp.StatusOK).decode(t, &got)
				if len(got.Contacts) != 1 || got.Contacts[0].Name != tc.want || got.Contacts[0].TenantID != tc.who.tenant.ID {
					t.Errorf("acc-1 in %s preloaded %+v, want %s alone", tc.who.tenant.ID, got.Contacts, tc.want)
				}
			}
		})

		// Each route's required permission is declared where the route is
		// registered, and the enforcement middleware decides it in the authorizing
		// stage: a role that holds the permission reaches the handler, and one that
		// does not is refused 403 by the middleware before the handler runs. The
		// handler reads no role, so these statuses are the policy's and not a
		// second check inside the handler.
		t.Run("each route takes its role", func(t *testing.T) {
			sub := c.in(t)
			vera.must(t, nethttp.MethodGet, "/accounts", nil, nethttp.StatusOK)
			vera.must(t, nethttp.MethodGet, "/accounts/acc-1", nil, nethttp.StatusOK)
			refused(t, "a viewer creating an account",
				vera.send(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-9", "name": "Viewer Co"}))
			refused(t, "a viewer updating an account",
				vera.send(t, nethttp.MethodPatch, "/accounts/acc-1", map[string]any{"name": "Viewer Co"}))
			refused(t, "a viewer changing an owner",
				vera.send(t, nethttp.MethodPost, "/accounts/acc-1/owner", map[string]any{"owner": cr.vera.ID}))
			refused(t, "a viewer deleting an account", vera.send(t, nethttp.MethodDelete, "/accounts/acc-1", nil))
			if rows := sub.accountRows("acc-9"); len(rows) != 0 {
				t.Errorf("a refused creation left %+v", rows)
			}

			rita.must(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-9", "name": "Rep Co"}, nethttp.StatusCreated)
			refused(t, "a rep deleting an account", rita.send(t, nethttp.MethodDelete, "/accounts/acc-9", nil))
			refused(t, "a rep purging an account", rita.send(t, nethttp.MethodPost, "/accounts/acc-9/purge", nil))
			ivan.must(t, nethttp.MethodDelete, "/accounts/acc-9", nil, nethttp.StatusNoContent)
			refused(t, "an admin purging an account", ivan.send(t, nethttp.MethodPost, "/accounts/acc-9/purge", nil))
			aliceAcme.must(t, nethttp.MethodPost, "/accounts/acc-9/purge", nil, nethttp.StatusNoContent)
			if rows := sub.accountRows("acc-9"); len(rows) != 0 {
				t.Errorf("the purge left %+v", rows)
			}
		})

		t.Run("a delete is soft, takes the contacts along and stays in the tenant", func(t *testing.T) {
			sub := c.in(t)
			ivan.must(t, nethttp.MethodDelete, "/accounts/acc-1", nil, nethttp.StatusNoContent)
			expect(t, "acme's acc-1 after its delete",
				aliceAcme.send(t, nethttp.MethodGet, "/accounts/acc-1", nil), nethttp.StatusNotFound)
			var other accountDetail
			aliceGlobex.must(t, nethttp.MethodGet, "/accounts/acc-1", nil, nethttp.StatusOK).decode(t, &other)
			if len(other.Contacts) != 1 || other.Contacts[0].Name != "Grace Hopper" {
				t.Errorf("globex's acc-1 after acme's delete preloads %+v, want Grace Hopper", other.Contacts)
			}

			stored := sub.accountRows("acc-1")
			if mine := stored[cr.acme.ID]; mine.name != "Rocket Works" || !mine.deleted {
				t.Errorf("acme's acc-1 is stored as %+v after its delete, want the row kept and marked deleted", mine)
			}
			if stored[cr.globex.ID].deleted {
				t.Errorf("globex's acc-1 is marked deleted by acme's delete")
			}
			contacts := sub.storedContacts("con-1")
			if !contacts[cr.acme.ID].deleted || contacts[cr.globex.ID].deleted {
				t.Errorf("con-1 is stored as %+v after acme deleted acc-1, want acme's deleted and globex's not", contacts)
			}
			// The deleted account's contacts are left out of every read, and
			// the other tenant's under the same identifiers are not.
			expect(t, "acme's con-1 after its account's delete",
				rita.send(t, nethttp.MethodGet, "/contacts/con-1", nil), nethttp.StatusNotFound)
			var atAccount contactList
			rita.must(t, nethttp.MethodGet, "/contacts?account_id=acc-1", nil, nethttp.StatusOK).decode(t, &atAccount)
			if len(atAccount.Contacts) != 0 {
				t.Errorf("acme lists %+v at acc-1 after its delete", atAccount.Contacts)
			}
			gus.must(t, nethttp.MethodGet, "/contacts/con-1", nil, nethttp.StatusOK)
			// The deleted account is gone for the single-row read a new
			// contact is checked against, so a contact cannot be put under it.
			expect(t, "a contact in acme at the deleted acc-1", rita.send(t, nethttp.MethodPost, "/contacts", map[string]any{
				"id": "con-3", "account_id": "acc-1", "name": "Late Comer", "email": "late@example.test",
			}), nethttp.StatusUnprocessableEntity)
			if rows := sub.storedContacts("con-3"); len(rows) != 0 {
				t.Errorf("a contact at the deleted acc-1 was stored: %+v", rows)
			}

			var page accountPage
			aliceAcme.must(t, nethttp.MethodGet, "/accounts", nil, nethttp.StatusOK).decode(t, &page)
			if page.Total != 3 {
				t.Errorf("acme counts %d accounts after the delete, want 3", page.Total)
			}
			expect(t, "a second delete of acme's acc-1",
				ivan.send(t, nethttp.MethodDelete, "/accounts/acc-1", nil), nethttp.StatusNotFound)
			expect(t, "a delete in acme of globex's acc-5",
				ivan.send(t, nethttp.MethodDelete, "/accounts/acc-5", nil), nethttp.StatusNotFound)
			if other := sub.accountRows("acc-5")[cr.globex.ID]; other.deleted {
				t.Errorf("globex's acc-5 is marked deleted by acme's delete of that identifier")
			}
		})

		t.Run("a purge removes the rows, deleted or not, in the tenant", func(t *testing.T) {
			sub := c.in(t)
			aliceAcme.must(t, nethttp.MethodPost, "/accounts/acc-1/purge", nil, nethttp.StatusNoContent)
			aliceAcme.must(t, nethttp.MethodPost, "/accounts/acc-2/purge", nil, nethttp.StatusNoContent)
			for _, id := range []string{"acc-1", "acc-2"} {
				stored := sub.accountRows(id)
				if _, left := stored[cr.acme.ID]; left {
					t.Errorf("acme's %s is still in the table after its purge", id)
				}
				if other, kept := stored[cr.globex.ID]; !kept || other.deleted {
					t.Errorf("globex's %s is %+v (kept %v) after acme's purge, want it untouched", id, other, kept)
				}
			}
			for _, id := range []string{"con-1", "con-2"} {
				if _, left := sub.storedContacts(id)[cr.acme.ID]; left {
					t.Errorf("acme's %s is still in the table after its account's purge", id)
				}
			}
			if _, kept := sub.storedContacts("con-1")[cr.globex.ID]; !kept {
				t.Errorf("globex's con-1 is gone after acme's purge")
			}
			expect(t, "a second purge of acme's acc-1",
				aliceAcme.send(t, nethttp.MethodPost, "/accounts/acc-1/purge", nil), nethttp.StatusNotFound)
			expect(t, "a purge in acme of globex's acc-5",
				aliceAcme.send(t, nethttp.MethodPost, "/accounts/acc-5/purge", nil), nethttp.StatusNotFound)
			if _, kept := sub.accountRows("acc-5")[cr.globex.ID]; !kept {
				t.Errorf("globex's acc-5 is gone after acme's purge of that identifier")
			}
		})

		t.Run("a delete that finds no account undoes its contacts' delete and their statement rows", func(t *testing.T) {
			sub := c.in(t)
			for _, who := range []actor{aliceAcme, aliceGlobex} {
				who.must(t, nethttp.MethodPost, "/accounts", map[string]any{"id": "acc-7", "name": "Halfway Ltd"},
					nethttp.StatusCreated)
				who.must(t, nethttp.MethodPost, "/contacts", map[string]any{
					"id": "con-7", "account_id": "acc-7", "name": "Hal Way", "email": "hal@halfway.test",
				}, nethttp.StatusCreated)
			}
			// An account deleted while a contact of it is live is a state no
			// route leaves behind, so the case writes it: the delete then
			// finds a contact to delete and no account, and answers 404 from
			// inside the transaction after the contacts' statement has run.
			sub.exec("UPDATE accounts SET deleted_at = ? WHERE tenant_id = ? AND id = ?",
				time.Now().UTC(), cr.acme.ID, "acc-7")
			before := sub.statementRows(ivan, "delete")
			expect(t, "a delete of acme's acc-7, marked deleted with its contact live",
				ivan.send(t, nethttp.MethodDelete, "/accounts/acc-7", nil), nethttp.StatusNotFound)
			sub.awaitJournal("the delete that found no account", func(r auditRow) bool {
				return r.requestOn(deleteAccountRoute) && r.outcome == "404" &&
					r.tenant == cr.acme.ID && r.actor == ivan.sessionActor()
			})

			// The journal writes a statement row inside the statement's own
			// transaction, so the rollback takes the rows of both statements
			// with it and none says a delete happened.
			if after := sub.statementRows(ivan, "delete"); !slices.Equal(after, before) {
				t.Errorf("a rolled-back delete left statement rows: %v before it, %v after", before, after)
			}
			stored := sub.storedContacts("con-7")
			if len(stored) != 2 || stored[cr.acme.ID].deleted || stored[cr.globex.ID].deleted {
				t.Errorf("con-7 is stored as %+v after a delete that found no account, want both tenants' live", stored)
			}
			rita.must(t, nethttp.MethodGet, "/contacts/con-7", nil, nethttp.StatusOK)
		})

		t.Run("every write route is journalled beside its statements, in the tenant that made them", func(t *testing.T) {
			sub := c.in(t)
			for _, want := range []struct {
				what                            string
				source, action, object, outcome string
				who                             actor
			}{
				{"the viewer's refused creation", "request", "POST", routePath(createAccountRoute), "403", vera},
				{"the viewer's refused update", "request", "PATCH", routePath(patchAccountRoute), "403", vera},
				{"the viewer's refused ownership change", "request", "POST", routePath(ownerAccountRoute), "403", vera},
				{"the viewer's refused delete", "request", "DELETE", routePath(deleteAccountRoute), "403", vera},
				{"the rep's refused delete", "request", "DELETE", routePath(deleteAccountRoute), "403", rita},
				{"the rep's refused purge", "request", "POST", routePath(purgeAccountRoute), "403", rita},
				{"the admin's refused purge", "request", "POST", routePath(purgeAccountRoute), "403", ivan},
				{"the rep's creation", "request", "POST", routePath(createAccountRoute), "201", rita},
				{"the owner's creation in globex", "request", "POST", routePath(createAccountRoute), "201", aliceGlobex},
				{"the owner's update", "request", "PATCH", routePath(patchAccountRoute), "200", aliceAcme},
				{"the owner's ownership change", "request", "POST", routePath(ownerAccountRoute), "200", aliceAcme},
				{"the admin's delete", "request", "DELETE", routePath(deleteAccountRoute), "204", ivan},
				{"the owner's purge", "request", "POST", routePath(purgeAccountRoute), "204", aliceAcme},
				{"the statement of the rep's creation", "statement", "create", accountTable, "", rita},
				{"the statement of the owner's creation in globex", "statement", "create", accountTable, "", aliceGlobex},
				{"the statement of the owner's updates", "statement", "update", accountTable, "", aliceAcme},
				{"the accounts statement of the admin's delete", "statement", "delete", accountTable, "", ivan},
				{"the contacts statement of the admin's delete", "statement", "delete", contactTable, "", ivan},
				{"the accounts statement of the owner's purge", "statement", "delete", accountTable, "", aliceAcme},
				{"the contacts statement of the owner's purge", "statement", "delete", contactTable, "", aliceAcme},
			} {
				sub.awaitJournal(want.what, func(r auditRow) bool {
					return r.source == want.source && r.action == want.action && r.object == want.object &&
						(want.outcome == "" || r.outcome == want.outcome) &&
						r.tenant == want.who.tenant.ID && r.actor == want.who.sessionActor()
				})
			}
		})
	})
}

// statementRows lists the journal's statement rows of one action an actor's
// requests left in the actor's tenant, as table and rows affected, oldest first.
func (c *crmCase) statementRows(who actor, action string) []string {
	c.t.Helper()
	rows, err := c.sql().Query(c.rebind("SELECT object, detail FROM audit_entries "+
		"WHERE source = 'statement' AND action = ? AND tenant_id = ? AND actor = ? ORDER BY id"),
		action, who.tenant.ID, who.sessionActor())
	if err != nil {
		c.t.Fatalf("reading the journal's statement rows: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var object, detail string
		if err := rows.Scan(&object, &detail); err != nil {
			c.t.Fatalf("scanning a statement row: %v", err)
		}
		out = append(out, object+" "+detail)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatalf("reading the journal's statement rows: %v", err)
	}
	return out
}

// requireNames fails a case unless a list of names is the one wanted.
func requireNames(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: got %s, want %s", what, strings.Join(got, ", "), strings.Join(want, ", "))
	}
}

// accountsGuardConfig is the configuration the enforcement-only accounts drive
// runs on: the endpoint the middleware would guard, named the way the stand-in
// entry point declares it.
const accountsGuardConfig = `{"authz":{"guard":{"endpoints":["api"]}}}`

// accountsSupply registers a stand-in module and supplies its instance, so the
// accounts module resolves the capability from it exactly as a host's assembly
// hands one over.
func accountsSupply(reg *core.Registry, name string, token core.Token, instance any) {
	reg.Register(core.Module{Name: name, Provides: []core.Provision{{Token: token}}})
	reg.Supply(instance, name)
}

// accountsEntryStub stands for the HTTP entry point in the accounts assembly
// cases: one endpoint whose registrations are dropped.
type accountsEntryStub struct{}

func (accountsEntryStub) Endpoint(string) (speedhttp.Endpoint, error) {
	return accountsEndpointStub{}, nil
}
func (accountsEntryStub) Endpoints() []string { return []string{apiEndpoint} }

// accountsEndpointStub accepts registrations and does nothing with them.
type accountsEndpointStub struct{}

func (accountsEndpointStub) Route(string, string, ...gin.HandlerFunc) {}
func (accountsEndpointStub) Use(speedhttp.Middleware)                 {}
func (accountsEndpointStub) Accepting() bool                          { return true }

// newAccountsRegistry builds a registry holding the real accounts module beside
// a route-declaration surface and stand-ins for every other capability the
// module requires, and names the modules a drive of it takes in.
//
// When guard is true the surface is the real enforcement module's, over the
// host's business vocabulary and a stand-in engine and catalog, so a drive to
// Start holds every declaration the accounts module makes to the permission
// catalog and to the resolved translation. When guard is false the surface is
// the given stand-in gate and no enforcement module runs, so a case reads back
// what the accounts module declared without the enforcement module's checks.
func newAccountsRegistry(t *testing.T, gate authz.Gate, guard bool) (*core.Registry, []string) {
	t.Helper()
	reg := core.New()
	reader := authztest.ConfigReaderAll(t, accountsGuardConfig, authzguard.Module())
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names := []string{accountsModuleName, "stand-in.router", "stand-in.db", "stand-in.settings", "stand-in.roster", "stand-in.memberships", "stand-in.log"}
	if guard {
		reg.Register(authzguard.Module())
		reg.Register(authzBusinessModule())
		accountsSupply(reg, "stand-in.enforcer", (*authz.Enforcer)(nil), authztest.NewEnforcer())
		accountsSupply(reg, "stand-in.catalog", (*authz.Catalog)(nil), authztest.NewCatalog(
			permAccountRead, permAccountWrite, permAccountDel, permAccountOwn, permAccountPurge))
		names = append(names, authzguard.ModuleName, authzBusinessModuleName, "stand-in.enforcer", "stand-in.catalog")
	} else {
		accountsSupply(reg, "stand-in.gate", (*authz.Gate)(nil), gate)
		names = append(names, "stand-in.gate")
	}
	reg.Register(accountsModule())
	accountsSupply(reg, "stand-in.router", (*speedhttp.Router)(nil), accountsEntryStub{})
	accountsSupply(reg, "stand-in.db", (*db.Database)(nil), struct{ db.Database }{})
	accountsSupply(reg, "stand-in.settings", (*settings.Store)(nil), struct{ settings.Store }{})
	accountsSupply(reg, "stand-in.roster", (*tenant.Catalog)(nil), struct{ tenant.Catalog }{})
	accountsSupply(reg, "stand-in.memberships", (*authn.Memberships)(nil), struct{ authn.Memberships }{})
	accountsSupply(reg, "stand-in.log", (*log.Logger)(nil), discardingLogger{})
	return reg, names
}

// accountRoutes is every route the accounts module binds on the API endpoint, in
// registration order, so a case that holds the whole surface to a property names
// them once.
var accountRoutes = []string{
	createAccountRoute, listAccountsRoute, getAccountRoute, patchAccountRoute,
	deleteAccountRoute, ownerAccountRoute, purgeAccountRoute,
}

// TestAccountsDeclaresEveryAPIRoute is the case for the accounts module's route
// declarations, the surface the enforcement middleware reads.
//
// Every account route on the API endpoint declares a permission, so a request
// that reaches one is decided by the policy rather than refused as an undeclared
// route: under the closed default a route with no declaration is answered 403
// for every caller, whatever role the caller holds. The observation therefore
// discriminates: with a `Require` dropped for a route, that route is undeclared
// and this case finds it not covered, while with the module as written every one
// is covered and a sufficient role passes the middleware. The drive runs the
// real enforcement module, so a declaration naming a permission the host does
// not declare, or one the resolved translation cannot express, fails the start
// rather than leaving a route that decides nothing at request time.
func TestAccountsDeclaresEveryAPIRoute(t *testing.T) {
	t.Parallel()
	reg, names := newAccountsRegistry(t, nil, true)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(_ context.Context, reg *core.Registry) error {
			gate, err := core.Resolve[authz.Gate](reg)
			if err != nil {
				return err
			}
			for _, route := range accountRoutes {
				method, path := splitRoute(route)
				if !gate.Covered(method, path) {
					t.Errorf("the route %s carries no declaration, so the middleware refuses it 403 for every caller "+
						"under the closed default", route)
				}
			}
			// The control: a route the module does not declare is not covered, which
			// is the state a dropped declaration leaves a real route in.
			if gate.Covered(nethttp.MethodGet, "/accounts/undeclared") {
				t.Error("a route the module does not declare reads as covered")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the accounts module with the enforcement module failed: %v", err)
	}
}

// TestAccountsDeclaresThePermissionEachRouteNeeds is the case for which
// permission each account route declares, and it is what makes the role matrix
// reach the routes: a viewer holds crm.account.read and so reads, a rep holds
// crm.account.write and so writes, an admin holds crm.account.delete, and an
// owner holds crm.account.own and crm.account.purge. A route that declared the
// wrong permission would let a role the matrix does not intend reach it, so each
// route is held to the exact permission it needs.
func TestAccountsDeclaresThePermissionEachRouteNeeds(t *testing.T) {
	t.Parallel()
	gate := authztest.NewGate()
	reg, names := newAccountsRegistry(t, gate, false)
	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageInit, Participants: names}); err != nil {
		t.Fatalf("driving the accounts module with a stand-in gate failed: %v", err)
	}
	for _, want := range []struct {
		route string
		perm  authz.Permission
	}{
		{createAccountRoute, permAccountWrite},
		{listAccountsRoute, permAccountRead},
		{getAccountRoute, permAccountRead},
		{patchAccountRoute, permAccountWrite},
		{deleteAccountRoute, permAccountDel},
		{ownerAccountRoute, permAccountOwn},
		{purgeAccountRoute, permAccountPurge},
	} {
		method, path := splitRoute(want.route)
		got, ok := gate.Requirement(method, path)
		if !ok {
			t.Errorf("the route %s declared no permission, want %q", want.route, want.perm)
			continue
		}
		if got != want.perm {
			t.Errorf("the route %s declares %q, want %q", want.route, got, want.perm)
		}
	}
}
