package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"maps"
	nethttp "net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/vislake/speed/pkg/audit/journal"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/ratelimit"
	"github.com/vislake/speed/pkg/tenant"
	"github.com/vislake/speed/pkg/tenant/resolve"
)

// The cases of the skeleton. Each claim in main.go's package comment has one
// here, or in wiring_test.go where the property leaves no trace in a run. A
// case that is about the host rather than about a stack runs on the stack that
// needs no docker; one whose property could differ between backends runs on
// every stack, as a parallel subtest per stack. A feature slice keeps its cases
// in a test file of its own.

// declaredTables are the tables every stack's migration has to build: every
// table a registered module's migrations create on the case's dialect, and the
// table of every model an application module lists. The first half is what the
// declarations say; the second catches a model whose table nothing builds.
func (c *crmCase) declaredTables() []string {
	c.t.Helper()
	tables, err := migrationTables(c.dialect)
	if err != nil {
		c.t.Fatalf("reading the declared migrations: %v", err)
	}
	for _, a := range applications {
		for _, model := range a.models {
			if named, ok := model.(interface{ TableName() string }); ok && !slices.Contains(tables, named.TableName()) {
				tables = append(tables, named.TableName())
			}
		}
	}
	sort.Strings(tables)
	return tables
}

// tables lists the tables in the case's database.
func (c *crmCase) tables() []string {
	c.t.Helper()
	query := "SELECT name FROM sqlite_master WHERE type = 'table'"
	if c.dialect == "postgres" {
		query = "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'"
	}
	rows, err := c.sql().Query(query)
	if err != nil {
		c.t.Fatalf("reading the schema: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			c.t.Fatalf("scanning a table name: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatalf("reading the schema: %v", err)
	}
	sort.Strings(names)
	return names
}

// requireTables fails the case unless every declared table exists.
func (c *crmCase) requireTables(after string) {
	c.t.Helper()
	got := c.tables()
	want := c.declaredTables()
	if len(want) == 0 {
		c.t.Fatal("no module declares a table, so this case checks nothing")
	}
	for _, table := range want {
		if !slices.Contains(got, table) {
			c.t.Errorf("%s left no %q table; the database holds %v", after, table, got)
		}
	}
}

// TestMigrateBuildsEveryDeclaredTable is the case for reaching a module for its
// declarations alone, for the commands past Migrate carrying the same
// declarations, and for a reference between two modules' tables.
//
// migrate runs the engine and nothing else, and every table is built. A
// command-line read then runs on a database nothing has migrated, and it builds
// every table as well: it drives past Migrate, and the static inclusion it
// inherits from the stack node is migrate's. A command whose declaring set were
// only what it runs would build the roster's and the journal's tables here and
// leave the users, memberships and settings tables unbuilt.
//
// The role table leads its key with the tenant column and refuses an empty one,
// which is the write a statement issued without the tenant filter would make.
// The membership relation itself is the account module's own table, migrated
// with that module.
func TestMigrateBuildsEveryDeclaredTable(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		c.requireTables("migrate")

		_, err := c.sql().Exec(c.rebind("INSERT INTO member_roles (tenant_id, account_id, role, granted_at) VALUES (?, ?, ?, ?)"),
			"", "u_nobody", roleRep, time.Now().UTC())
		if err == nil {
			t.Errorf("a member role with an empty tenant was written: the scoped key is not enforced")
		}

		other := newCase(t, c.stack)
		other.mustRun("", "tenant", "list")
		other.requireTables("tenant list on an unmigrated database")
	})
}

// TestEveryStackServesTheSamePath is the case for one binary on five stacks:
// each goes through the same path and answers the same way.
//
// Two tenants and two users are put in place at the command line, one user in
// each tenant, and then served. The user signs in on the account endpoint and
// the token reaches the API; the user's Basic credential reaches no backend on
// the API, since the one scheme belongs to the operators' table; the account
// endpoint answers who the caller is across tenants; the second user sees their
// own tenant; the owner grants from the API; and a logout ends the token.
func TestEveryStackServesTheSamePath(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		globex := c.createTenant("globex")
		alice := c.createUser("alice")
		bob := c.createUser("bob")
		c.grant(acme, alice, roleOwner)
		c.grant(globex, bob, roleRep)
		h := c.serve()

		// A login runs through the flow routes, which are public: they are
		// mounted at the base path on the account endpoint, and a login that
		// completes sets the session cookie. A credential that does not sign
		// anyone in is answered by the flow handler, not the gateway.
		wrong := h.post(t, accountEndpoint, loginFlowPath, "", noCredential{},
			map[string]any{"method": "password", "input": map[string]string{"identifier": alice.Email, "password": "not it"}})
		expect(t, "a login with the wrong password", wrong, nethttp.StatusUnauthorized)

		token := h.login(t, alice)

		viaSession := h.members(t, acme.Alias, bearer(token))
		// The context carries the identifier, never the alias the request
		// named: renaming a tenant rewrites no row.
		if viaSession.Tenant != acme.ID {
			t.Errorf("the listing ran in tenant %q, want the identifier %q behind the alias %q",
				viaSession.Tenant, acme.ID, acme.Alias)
		}
		if want := alice.ID; viaSession.Caller != want {
			t.Errorf("a signed-in request was answered for %q, want %q: one person is one identity", viaSession.Caller, want)
		}
		if viaSession.Role != roleOwner {
			t.Errorf("the caller's role reads %q, want %q", viaSession.Role, roleOwner)
		}
		requireMembers(t, viaSession, alice)

		// A CRM user's Basic credential reaches no backend: the gateway hands
		// Basic to the one backend that takes it, the operators' table, which
		// does not know the user and refuses the credential.
		expect(t, "a CRM user's Basic credential on the API",
			h.get(t, apiEndpoint, "/members", acme.Alias, basic(alice)), nethttp.StatusUnauthorized)

		me := h.me(t, bearer(token))
		if me.Account == nil || me.Account.ID != alice.ID || me.Account.Email != alice.Email {
			t.Errorf("GET /me answered account %+v, want %s (%s)", me.Account, alice.ID, alice.Email)
		}
		wantTenants := []meTenant{{ID: acme.ID, Alias: acme.Alias, Status: "active", Role: roleOwner}}
		if !slices.Equal(me.Tenants, wantTenants) {
			t.Errorf("GET /me answered the tenants %+v, want %+v", me.Tenants, wantTenants)
		}

		bobs := h.members(t, globex.Alias, signedIn(bob))
		if bobs.Tenant != globex.ID || bobs.Role != roleRep {
			t.Errorf("bob's listing ran in %q as %q, want %q as %q", bobs.Tenant, bobs.Role, globex.ID, roleRep)
		}
		requireMembers(t, bobs, bob)

		// A subtest of its own, so that what it finds does not end the
		// rest of the path.
		t.Run("the owner grants from the API, and a second grant rewrites the role", func(t *testing.T) {
			// The role row the grant writes names no tenant: the filter fills
			// it from the request's context. The first grant inserts the
			// row and the second rewrites the role on it. The roles differ
			// from the one bob holds in globex, so a grant whose conflict
			// landed on globex's row would show there.
			for _, role := range []string{roleAdmin, roleViewer} {
				granted := h.post(t, apiEndpoint, "/members", acme.Alias, bearer(token),
					map[string]string{"email": bob.Email, "role": role})
				expect(t, "alice granting bob "+role+" in acme", granted, nethttp.StatusOK)
				var got grantView
				granted.decode(t, &got)
				if got.TenantID != acme.ID || got.AccountID != bob.ID || got.Role != role {
					t.Errorf("the grant answered %+v, want %s in %s as %s", got, bob.ID, acme.ID, role)
				}
			}
			inAcme := h.members(t, acme.Alias, signedIn(bob))
			requireMembers(t, inAcme, alice, bob)
			if inAcme.Role != roleViewer {
				t.Errorf("bob's role in acme reads %q after the second grant, want %q", inAcme.Role, roleViewer)
			}
			// The grant stayed in acme: bob's membership in globex is the
			// one the command line wrote, and globex still holds bob alone.
			inGlobex := h.members(t, globex.Alias, signedIn(bob))
			requireMembers(t, inGlobex, bob)
			if inGlobex.Role != roleRep {
				t.Errorf("bob's role in globex reads %q after the grants in acme, want the %q the command line wrote",
					inGlobex.Role, roleRep)
			}
			expect(t, "bob, not an owner or an admin of acme, granting there",
				h.post(t, apiEndpoint, "/members", acme.Alias, signedIn(bob),
					map[string]string{"email": alice.Email, "role": roleViewer}), nethttp.StatusForbidden)
		})

		requireSpanExported(t, c, h)

		expect(t, "a logout with the session", h.post(t, accountEndpoint, logoutPath, "", bearer(token), nil),
			nethttp.StatusNoContent)
		expect(t, "the session after its logout", h.get(t, accountEndpoint, "/me", "", bearer(token)),
			nethttp.StatusUnauthorized)
	})
}

// requireSpanExported waits for the API requests' server span to leave through
// the export backend the stack names: the in-process receiver for the OTLP
// backend, which decodes it, the run's standard output for the stdout one. The
// span is named for the method and the route the telemetry layer observed,
// which is a string nothing else in the run writes.
func requireSpanExported(t *testing.T, c *crmCase, h *crmHost) {
	t.Helper()
	name := nethttp.MethodGet + " /members"
	if c.receiver != nil {
		if !eventually(settleLimit, func() bool { return len(c.receiver.spansNamed(name)) > 0 }) {
			t.Errorf("no span named %s reached the OTLP receiver after the requests were served", name)
		}
		c.receiver.requireDecoded(t)
		return
	}
	spanName := `"Name":"` + name + `"`
	if !eventually(settleLimit, func() bool { return strings.Contains(h.output(), spanName) }) {
		t.Errorf("no span named %s reached the run's standard output; it wrote:\n%s", spanName, h.outputForReport())
	}
}

// sessionCheckTTLSetting is the session module's status-cache lifetime. A case
// that changes an account's row and reads the change on the next request sets
// it to zero, so nothing is cached.
const sessionCheckTTLSetting = "authn.session.check-ttl"

// TestTwoReplicasShareTheirSessions is the case for replicas on shared
// backends. The two are started together against one database and one
// key-value store, as a rollout starts them; a session cookie one replica
// issues is honoured by the other, because the session lives in the store and
// not in the
// process, and a logout on the second ends the session on the first.
func TestTwoReplicasShareTheirSessions(t *testing.T) {
	t.Parallel()
	forEachSharedStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleRep)
		replicas := c.serveReplicas(2, together)
		first, second := replicas[0], replicas[1]

		token := first.login(t, alice)
		list := second.members(t, acme.Alias, bearer(token))
		if want := alice.ID; list.Caller != want {
			t.Errorf("the second replica answered a session the first issued for %q, want %q", list.Caller, want)
		}
		expect(t, "a logout on the second replica",
			second.post(t, accountEndpoint, logoutPath, "", bearer(token), nil), nethttp.StatusNoContent)
		expect(t, "the session on the first replica after the logout on the second",
			first.get(t, accountEndpoint, "/me", "", bearer(token)), nethttp.StatusUnauthorized)
	})
}

// TestTheAdminEndpointActsOnTheTableFromAnyReplica is the case for the roster
// seen from a replica that has not reread it. The roster's refresh period is
// zero, so the second replica holds the roster as it read it at startup, before
// the first registered anything.
//
// A status change is the roster's to validate against its table, so it reaches
// a tenant the second replica has never read. A registration of an identifier
// the table already holds is refused wherever it is made: the roster's write
// would rewrite that tenant's alias and status.
//
// The two replicas are started one after the other: what this case is about is
// not the rollout.
func TestTheAdminEndpointActsOnTheTableFromAnyReplica(t *testing.T) {
	t.Parallel()
	forEachSharedStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		replicas := c.serveReplicas(2, firstThenRest)
		writer, stale := replicas[0], replicas[1]

		t.Run("a status change on another replica reaches the table", func(t *testing.T) {
			sub := c.in(t)
			ref := sub.tenantNamed("acme")
			writer.adminCreateTenant(t, ref)
			changed := stale.post(t, adminEndpoint, "/tenants/"+ref.ID+"/disable", "", operator(), nil)
			expect(t, "disabling, on the other replica, a tenant the first registered", changed, nethttp.StatusOK)
			var entry tenantView
			changed.decode(t, &entry)
			if entry.ID != ref.ID || entry.Status != "disabled" {
				t.Errorf("the disable answered %+v, want %s disabled", entry, ref.ID)
			}
			if alias, status := sub.rosterRow(ref.ID); alias != ref.Alias || status != int(tenant.StatusDisabled) {
				t.Errorf("the roster's row reads alias %q status %d, want %q disabled", alias, status, ref.Alias)
			}
		})

		t.Run("a registration on another replica refuses an identifier the roster holds", func(t *testing.T) {
			sub := c.in(t)
			ref := sub.tenantNamed("globex")
			writer.adminCreateTenant(t, ref)
			expect(t, "disabling it on the first replica",
				writer.post(t, adminEndpoint, "/tenants/"+ref.ID+"/disable", "", operator(), nil), nethttp.StatusOK)
			again := stale.post(t, adminEndpoint, "/tenants", "", operator(),
				map[string]string{"id": ref.ID, "alias": "renamed-" + sub.suffix})
			if again.status != nethttp.StatusConflict {
				t.Errorf("registering, on the other replica, an identifier the roster holds answered %d %s, want %d",
					again.status, again.body, nethttp.StatusConflict)
			}
			if alias, status := sub.rosterRow(ref.ID); alias != ref.Alias || status != int(tenant.StatusDisabled) {
				t.Errorf("the roster's row reads alias %q status %d after the second registration, want %q disabled "+
					"as the first replica left it", alias, status, ref.Alias)
			}
		})
	})
}

// rosterRow reads one tenant's row of the roster's table.
func (c *crmCase) rosterRow(id string) (alias string, status int) {
	c.t.Helper()
	if err := c.sql().QueryRow(c.rebind("SELECT alias, status FROM tenants WHERE id = ?"), id).Scan(&alias, &status); err != nil {
		c.t.Fatalf("reading the roster's row for %s: %v", id, err)
	}
	return alias, status
}

// requireMembers fails the case unless a listing holds exactly these users.
// The listing's query carries no tenant condition, so a run without the filter
// returns the other tenant's member as well and fails here on the count.
func requireMembers(t *testing.T, got memberList, want ...userRef) {
	t.Helper()
	var ids, wantIDs []string
	for _, m := range got.Members {
		ids = append(ids, m.AccountID)
	}
	for _, u := range want {
		wantIDs = append(wantIDs, u.ID)
	}
	sort.Strings(ids)
	sort.Strings(wantIDs)
	if !slices.Equal(ids, wantIDs) {
		t.Errorf("the listing of %s holds %v, want %v", got.Tenant, ids, wantIDs)
	}
}

// TestTheRequestSurfaceKeepsTenantsApart is the case for isolation where a
// caller can reach it: the admission layer, the gateway before resolution, the
// two tenant refusals being one answer, the admin endpoint's two audiences,
// and a disabled user.
func TestTheRequestSurfaceKeepsTenantsApart(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		globex := c.createTenant("globex")
		initech := c.createTenant("initech")
		c.disableTenant(initech)
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		// No caching of a session's status or its account, so a change to an
		// account's row is seen on the next request.
		c.setGlobal(sessionCheckTTLSetting, "0s")
		h := c.serve()

		t.Run("a member naming another tenant is refused, and the refusal is journalled", func(t *testing.T) {
			sub := c.in(t)
			resp := h.get(t, apiEndpoint, "/members", globex.Alias, signedIn(alice))
			expect(t, "alice naming globex", resp, nethttp.StatusNotFound)
			requireProblem(t, "the admission layer's refusal", resp)
			// The layer sits inside the journal, so its refusal is a request
			// row: the route, the status, the tenant the request named and
			// the caller who named it.
			sub.awaitJournal("the admission layer's refusal", func(r auditRow) bool {
				return r.requestOn(listMembersRoute) && r.outcome == "404" &&
					r.tenant == globex.ID && r.actor == alice.ID
			})
		})

		t.Run("a request with no credential is refused before resolution", func(t *testing.T) {
			sub := c.in(t)
			unknown := sub.tenantNamed("nosuch").Alias
			unauthenticated := h.get(t, apiEndpoint, "/members", unknown, noCredential{})
			expect(t, "no credential naming an unknown tenant", unauthenticated, nethttp.StatusUnauthorized)
			requireProblem(t, "the gateway's refusal", unauthenticated)
			// The same tenant with a credential is resolution's to refuse,
			// which is what tells the two layers apart by the answer: a
			// gateway inside resolution would have answered 404 above.
			unresolved := h.get(t, apiEndpoint, "/members", unknown, signedIn(alice))
			expect(t, "a credential naming an unknown tenant", unresolved, nethttp.StatusNotFound)
			requireProblem(t, "resolution's refusal", unresolved)
		})

		t.Run("a disabled tenant and an unknown one get the same answer", func(t *testing.T) {
			sub := c.in(t)
			disabled := h.get(t, apiEndpoint, "/members", initech.Alias, signedIn(alice))
			unknown := h.get(t, apiEndpoint, "/members", sub.tenantNamed("nosuch").Alias, signedIn(alice))
			// Asserted exactly, not merely as "the same": two answers that
			// agree because both fell through to one 403 would be equal without
			// the roster having judged anything.
			expect(t, "a request naming a disabled tenant", disabled, nethttp.StatusNotFound)
			if disabled.status != unknown.status || string(disabled.body) != string(unknown.body) {
				t.Errorf("a disabled tenant answered %d %q and an unknown one %d %q; telling them apart enumerates the roster",
					disabled.status, disabled.body, unknown.status, unknown.body)
			}
		})

		t.Run("a non-member and an unknown tenant get the same shape", func(t *testing.T) {
			// globex is a real tenant alice is not a member of, so the admission
			// layer answers it; a tenant no one registered is resolution's to
			// answer. Both are 404 problem details, so a caller cannot tell a
			// tenant that does not hold them from one that does not exist.
			nonMember := h.get(t, apiEndpoint, "/members", globex.Alias, signedIn(alice))
			unknown := h.get(t, apiEndpoint, "/members", c.in(t).tenantNamed("none").Alias, signedIn(alice))
			expect(t, "a non-member naming globex", nonMember, nethttp.StatusNotFound)
			expect(t, "a caller naming an unknown tenant", unknown, nethttp.StatusNotFound)
			requireProblem(t, "the admission layer's refusal", nonMember)
			requireProblem(t, "resolution's refusal", unknown)
			if got, want := nonMember.header.Get("Content-Type"), unknown.header.Get("Content-Type"); got != want {
				t.Errorf("a non-member's refusal carries %q and an unknown tenant's %q; the two have to read alike",
					got, want)
			}
		})

		t.Run("an operator registers a tenant that is usable at once", func(t *testing.T) {
			sub := c.in(t)
			fresh := sub.tenantNamed("hooli")
			h.adminCreateTenant(t, fresh)
			carol := h.adminCreateUser(t, sub.userNamed("carol"))
			h.adminGrant(t, fresh, carol, roleAdmin)

			// The roster is reread by the replica that wrote it, so the tenant
			// resolves on the next request with no refresh period in between.
			list := h.members(t, fresh.Alias, signedIn(carol))
			if list.Tenant != fresh.ID || list.Role != roleAdmin {
				t.Errorf("carol's listing ran in %q as %q, want %q as %q", list.Tenant, list.Role, fresh.ID, roleAdmin)
			}
			requireMembers(t, list, carol)

			// The grant crossed tenants from an endpoint that resolves none,
			// and the seam is where it says so: the operator is the actor and
			// the purpose is stated.
			seam := sub.awaitJournal("the operator's grant", func(r auditRow) bool {
				return r.source == "seam" && r.action == "membership.grant" &&
					r.actor == string(operatorAccount) && strings.Contains(r.object, fresh.ID)
			})
			if seam.purpose == "" {
				t.Errorf("the grant's seam row states no purpose: %+v", seam)
			}

			// Disabling the tenant from the admin endpoint refuses it on the
			// next request, and enabling it brings it back: the replica that
			// wrote the roster rereads it at once.
			for _, step := range []struct {
				route, status string
				want          int
			}{
				{"/disable", "disabled", nethttp.StatusNotFound},
				{"/enable", "active", nethttp.StatusOK},
			} {
				changed := h.post(t, adminEndpoint, "/tenants/"+fresh.ID+step.route, "", operator(), nil)
				expect(t, "the operator's "+step.route, changed, nethttp.StatusOK)
				var entry tenantView
				changed.decode(t, &entry)
				if entry.ID != fresh.ID || entry.Status != step.status {
					t.Errorf("%s answered %+v, want %s %s", step.route, entry, fresh.ID, step.status)
				}
				expect(t, "carol's listing after "+step.route,
					h.get(t, apiEndpoint, "/members", fresh.Alias, signedIn(carol)), step.want)
			}
			// A status change for an identifier no tenant has is not found,
			// and one for an identifier no tenant could have is refused as
			// malformed.
			expect(t, "disabling a tenant nobody registered",
				h.post(t, adminEndpoint, "/tenants/"+sub.tenantNamed("nosuch").ID+"/disable", "", operator(), nil),
				nethttp.StatusNotFound)
			expect(t, "disabling a malformed identifier",
				h.post(t, adminEndpoint, "/tenants/%20/disable", "", operator(), nil), nethttp.StatusUnprocessableEntity)
		})

		t.Run("a CRM user on the admin endpoint is refused", func(t *testing.T) {
			sub := c.in(t)
			expect(t, "alice, signed in, on the admin endpoint",
				h.get(t, adminEndpoint, "/tenants", "", signedIn(alice)), nethttp.StatusForbidden)
			// The operator check sits inside the journal, so a CRM user
			// knocking on the admin endpoint leaves a request row with the
			// caller named.
			sub.awaitJournal("the operator check's refusal", func(r auditRow) bool {
				return r.requestOn(listTenantsRoute) && r.outcome == "403" &&
					r.actor == alice.ID
			})
			token := h.login(t, alice)
			expect(t, "alice's token on the admin endpoint",
				h.get(t, adminEndpoint, "/tenants", "", bearer(token)), nethttp.StatusForbidden)
			// The operator is not refused, so the refusal above is about
			// who asked rather than about the route.
			expect(t, "the operator on the admin endpoint",
				h.get(t, adminEndpoint, "/tenants", "", operator()), nethttp.StatusOK)
		})

		// Last, because it leaves alice unable to sign in.
		t.Run("a disabled account is refused on every session it holds and on a new login", func(t *testing.T) {
			sub := c.in(t)
			token := h.login(t, alice)
			expect(t, "alice before she is disabled", h.get(t, accountEndpoint, "/me", "", signedIn(alice)), nethttp.StatusOK)
			sub.exec("UPDATE authn_accounts SET status = ? WHERE id = ?", 0, alice.ID)
			expect(t, "a disabled account's session the replica holds",
				h.get(t, accountEndpoint, "/me", "", signedIn(alice)), nethttp.StatusUnauthorized)
			expect(t, "a disabled account's session",
				h.get(t, accountEndpoint, "/me", "", bearer(token)), nethttp.StatusUnauthorized)
			expect(t, "a disabled account's login",
				h.post(t, accountEndpoint, loginFlowPath, "", noCredential{},
					map[string]any{
						"method": "password",
						"input":  map[string]string{"identifier": alice.Email, "password": alice.Password},
					}),
				nethttp.StatusUnauthorized)
		})
	})
}

// TestTwoCreationsOfOneEmailAreOneSuccessAndOneConflict is the case for a
// duplicate that the look-up before the insert cannot see. Two creations of one
// address arrive together; each derives a password hash between its look-up and
// its insert, so both find the address free, and the unique constraint is what
// refuses the second. That refusal is a conflict, as a duplicate found by the
// look-up is, and never a server error, and it is answered with the body the
// look-up's is, which a third creation of the address, made once the first is
// written, is answered with: the constraint's name and the engine's code are
// in the driver's error alone, and no answer carries them. The creation takes a
// plaintext password, so the derivation it runs is what holds the two apart: a
// creation that imported an entry without deriving could find the first at its
// look-up. The operator's credential is checked once before the two arrive:
// the static backend derives at most as many hashes at once as it has
// processors, so two requests that both find its cache cold would, on one
// processor, have one of them answered 503 and no longer be the two creations
// the case is about.
func TestTwoCreationsOfOneEmailAreOneSuccessAndOneConflict(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()
		u := c.userNamed("twice")
		body := map[string]string{"email": u.Email, "display_name": "twice", "password": u.Password}
		expect(t, "the operator's first request", h.get(t, adminEndpoint, "/tenants", "", operator()), nethttp.StatusOK)

		answers := make([]response, 2)
		failures := make([]error, 2)
		var wg sync.WaitGroup
		for i := range answers {
			wg.Go(func() {
				answers[i], failures[i] = h.do(request{
					method: nethttp.MethodPost, endpoint: adminEndpoint, path: "/users", cred: operator(), body: body,
				})
			})
		}
		wg.Wait()
		for _, err := range failures {
			if err != nil {
				t.Fatalf("a creation could not be sent: %v", err)
			}
		}
		statuses := []int{answers[0].status, answers[1].status}
		slices.Sort(statuses)
		if want := []int{nethttp.StatusCreated, nethttp.StatusConflict}; !slices.Equal(statuses, want) {
			t.Fatalf("two creations of %s at once answered %d %s and %d %s, want one %d and one %d",
				u.Email, answers[0].status, answers[0].body, answers[1].status, answers[1].body,
				nethttp.StatusCreated, nethttp.StatusConflict)
		}
		third := h.post(t, adminEndpoint, "/users", "", operator(), body)
		expect(t, "a third creation of "+u.Email, third, nethttp.StatusConflict)
		for _, a := range answers {
			if a.status == nethttp.StatusConflict && string(a.body) != string(third.body) {
				t.Errorf("the conflict of the two creations at once answered %s, and the one the look-up "+
					"finds answers %s", a.body, third.body)
			}
		}
	})
}

// TestAWeakPasswordIsRefusedAtCreation is the case for the password policy at
// the admin endpoint: a creation whose password is shorter than the policy
// allows is answered 422 and writes no account, so the policy the account module
// judges is the one the host's create path reports, and a creation that failed it
// leaves nothing behind.
func TestAWeakPasswordIsRefusedAtCreation(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()
		u := c.userNamed("weak")
		resp := h.post(t, adminEndpoint, "/users", "", operator(),
			map[string]string{"email": u.Email, "display_name": "weak", "password": "short"})
		expect(t, "a creation whose password is below the policy", resp, nethttp.StatusUnprocessableEntity)
		if n := c.count("SELECT count(*) FROM authn_accounts WHERE display_name = ?", "weak"); n != 0 {
			t.Fatalf("the accounts table holds %d rows after a refused creation, want none", n)
		}
	})
}

// TestACommandLineGrantWithNoKnownRoleWritesNothing is the case for the order the
// command line's grant judges its inputs in: a role that is none of the four is
// refused before any membership is written, so a failed command leaves no
// half-written membership behind. The HTTP grant judges the same role in its
// request body's validation, before it writes.
func TestACommandLineGrantWithNoKnownRoleWritesNothing(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")

		res := c.run(nil, "", "user", "grant", "--tenant-id", acme.ID, "--email", alice.Email, "--role", "bogus")
		if res.err == nil {
			t.Fatalf("user grant with an unknown role succeeded:\n%s", res.combined())
		}
		if !strings.Contains(res.stderr, errUnknownRole.Error()) {
			t.Errorf("user grant's failure does not name the role; it wrote:\n%s", res.combined())
		}
		if n := c.count("SELECT count(*) FROM authn_memberships WHERE account_id = ?", alice.ID); n != 0 {
			t.Errorf("the refused grant wrote %d memberships, want none", n)
		}
		if n := c.count("SELECT count(*) FROM member_roles WHERE account_id = ?", alice.ID); n != 0 {
			t.Errorf("the refused grant wrote %d roles, want none", n)
		}
	})
}

// TestAPasswordIsDerivedAtTheAccountPolicy is the case for the cost a CRM user's
// password is derived at. The served deployment reads its policy from the
// settings store, so a user created at the admin endpoint stores the combination
// the deployment configured, and one created under a policy signs in once the
// deployment gives another, that sign-in storing the entry again at the
// combination now set. A command-line creation runs no settings store — a command
// group carries no event backend, and the settings store requires one — so it
// derives at the module's declared defaults. A password below the minimum length
// is refused at the command line, leaving no account behind.
func TestAPasswordIsDerivedAtTheAccountPolicy(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	// The first policy is OWASP's one-iteration floor pair, distinct from the
	// module's default two-iteration pair; the settings store loads its snapshot
	// at startup, so each policy is written before the run that reads it.
	c.setGlobal("authn.account.password-argon2-memory-kib", "47104")
	c.setGlobal("authn.account.password-argon2-iterations", "1")
	c.setGlobal("authn.account.password-argon2-parallelism", "1")
	h := c.serve()
	early := h.adminCreateUser(t, c.userNamed("early"))
	if got := c.text("SELECT phc FROM authn_password_credentials WHERE account_id = ?", early.ID); !strings.Contains(got, "m=47104,t=1,p=1") {
		t.Errorf("a user created at the admin endpoint stores %q, want the policy combination m=47104,t=1,p=1", got)
	}
	h.stop(t)

	// The deployment moves to another floor pair, and the user created under the
	// first signs in once it does.
	c.setGlobal("authn.account.password-argon2-memory-kib", "12288")
	c.setGlobal("authn.account.password-argon2-iterations", "3")
	c.setGlobal("authn.account.password-argon2-parallelism", "1")
	h = c.serve()
	h.login(t, early)
	if got := c.text("SELECT phc FROM authn_password_credentials WHERE account_id = ?", early.ID); !strings.Contains(got, "m=12288,t=3,p=1") {
		t.Errorf("the entry after a sign-in under the moved policy is %q, want it stored again at m=12288,t=3,p=1", got)
	}
	h.stop(t)

	// A command-line creation runs no settings store, so it derives at the
	// module's declared defaults, not at the policy the deployment set.
	cli := c.userNamed("cli")
	var created userView
	c.resultOf(c.mustRun(cli.Password+"\n", "user", "create",
		"--email", cli.Email, "--display-name", "cli"), &created)
	if got := c.text("SELECT phc FROM authn_password_credentials WHERE account_id = ?", created.ID); !strings.Contains(got, "m=19456,t=2,p=1") {
		t.Errorf("a user created at the command line stores %q, want the module's default combination m=19456,t=2,p=1", got)
	}

	// A weak password is refused there too, before any account is written.
	weak := c.userNamed("weak")
	if res := c.run(nil, "short\n", "user", "create", "--email", weak.Email, "--display-name", "weak"); res.err == nil {
		t.Fatalf("a command-line creation whose password is below the policy succeeded:\n%s", res.combined())
	}
	if n := c.count("SELECT count(*) FROM authn_accounts WHERE display_name = ?", "weak"); n != 0 {
		t.Errorf("the refused command-line creation wrote %d accounts, want none", n)
	}
}

// TestASessionEndsWhenItsIdleTimeoutLapses is the case for the session lifetime:
// the setting the session module declares, read from the settings surface at
// login, and honoured by the key-value backend the stack names.
//
// The idle timeout is the session setting a browser session an unused cookie is
// refused by, and the shortest one, so it is written at its domain minimum. Its
// period is extended by use, but only once half of it has passed since the last
// write, so one check is made well inside that half — where use writes nothing —
// and the next only after the period has ended. A cookie honoured at once and at
// that check and refused after is neither a store keeping it for ever nor one
// reading the setting in another unit.
func TestASessionEndsWhenItsIdleTimeoutLapses(t *testing.T) {
	const idle = time.Minute
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleRep)
		c.setGlobal("authn.session.idle-timeout", idle.String())
		h := c.serve()

		issuedAt := time.Now()
		cookie := h.login(t, alice)
		expect(t, "the session at once", h.get(t, accountEndpoint, "/me", "", bearer(cookie)), nethttp.StatusOK)

		// Idle/2 is a minute, so a use this far in writes nothing and leaves the
		// period ending a minute after the login.
		time.Sleep(time.Until(issuedAt.Add(idle/2 - 2*time.Second)))
		expect(t, "the session half-way through its idle period",
			h.get(t, accountEndpoint, "/me", "", bearer(cookie)), nethttp.StatusOK)

		// No use is made of the cookie until the period has ended: a use inside it
		// would extend it.
		time.Sleep(time.Until(issuedAt.Add(idle + 2*lifetimeSlack)))
		expect(t, "the session past its idle timeout",
			h.get(t, accountEndpoint, "/me", "", bearer(cookie)), nethttp.StatusUnauthorized)
	})
}

// The refusal the hash case adds to the accounts table: its name, and the display
// name it refuses.
const (
	userRefusalName    = "crm_case_refuses_users"
	refusedDisplayName = "refused by the case"
)

// TestAPasswordHashReachesNoOutput is the case for where a CRM user's password
// hash goes, on every stack: into the account store's credential column and
// nowhere else. The case has the database refuse one creation, through a
// refusal it adds to the accounts table once the migration has run — a CHECK on
// PostgreSQL, a trigger on SQLite, neither of which the host knows of — and the
// refused creation is
// answered 500 and leaves neither the hash it derived nor the address in the
// serving process's output.
//
// The database module keeps a logger on the handle it delivers, and that
// logger writes a statement that failed as a record, through the logging
// module and without the values bound into it, so the refused insert is
// recorded neither with the hash nor with the address. The host does not route
// its statements around the logger the delivered handle carries: the claim is
// the module's, and this case holds it. The record is looked for before the
// output is searched, so an output the database wrote nothing of the refused
// insert to does not pass for one that holds it without the values.
func TestAPasswordHashReachesNoOutput(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		if c.dialect == "postgres" {
			c.exec("ALTER TABLE authn_accounts ADD CONSTRAINT " + userRefusalName +
				" CHECK (display_name <> '" + refusedDisplayName + "')")
		} else {
			c.exec("CREATE TRIGGER " + userRefusalName + " BEFORE INSERT ON authn_accounts WHEN NEW.display_name = '" +
				refusedDisplayName + "' BEGIN SELECT RAISE(ABORT, 'the case refuses this user'); END")
		}
		h := c.serve()
		u := c.userNamed("refused")
		resp := h.post(t, adminEndpoint, "/users", "", operator(),
			map[string]string{"email": u.Email, "display_name": refusedDisplayName, "password": u.Password})
		expect(t, "a creation the database refuses", resp, nethttp.StatusInternalServerError)
		if n := c.count("SELECT count(*) FROM authn_accounts WHERE display_name = ?", refusedDisplayName); n != 0 {
			t.Fatalf("the accounts table holds %d rows for the refused name after the creation, want none", n)
		}
		// The run's output is whole once it has stopped.
		h.stop(t)
		out := h.output()
		if _, ok := findRecord(out, "creating a user"); !ok {
			t.Fatalf("the serving process recorded no failure of the refused creation, so the refusal was never met")
		}
		// The database's own record of the refused insert has to be there, or the scan
		// below reads a stream that never held the statement and says nothing about
		// what the module's logger leaves out of it.
		recorded := slices.ContainsFunc(records(out), func(rec record) bool {
			return rec.is("statement failed", "level", "ERROR", "module", c.stack.engine) &&
				strings.Contains(rec["statement"], "INSERT INTO") && strings.Contains(rec["statement"], "authn_")
		})
		if !recorded {
			t.Fatalf("the serving process's output carries no \"statement failed\" record at ERROR, stamped "+
				"with module=%s, for an insert into an authn table, so the database module recorded nothing "+
				"of the refused insert:\n%s", c.stack.engine, tail(out))
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "$argon2id$") {
				t.Errorf("the serving process's output carries a password hash: %s", line)
			}
			if strings.Contains(line, u.Email) {
				t.Errorf("the serving process's output carries the refused user's address: %s", line)
			}
		}
	})
}

// TestACommandLineWriteStatesItsPurpose is the case for the command line's
// record. The command line carries no identity and no tenant, so the journal's
// statement rows say that something was written and not why; each write also
// records a seam row with its purpose. The seam accepts records from the
// journal's Start on, so a command that stopped at Init could not write one,
// and every write here succeeding is the depth being right.
func TestACommandLineWriteStatesItsPurpose(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	acme := c.createTenant("acme")
	c.disableTenant(acme)
	var enabled tenantView
	c.resultOf(c.mustRun("", "tenant", "enable", "--tenant-id", acme.ID), &enabled)
	if enabled.Status != "active" {
		t.Errorf("tenant enable answered %+v", enabled)
	}
	alice := c.createUser("alice")
	c.grant(acme, alice, roleViewer)

	var listed []userListing
	c.resultOf(c.mustRun("", "user", "list"), &listed)
	if len(listed) != 1 || listed[0].ID != alice.ID || len(listed[0].Memberships) != 1 ||
		listed[0].Memberships[0].TenantID != acme.ID {
		t.Errorf("user list answered %+v, want alice with her membership in %s", listed, acme.ID)
	}

	want := map[string]int{"tenant.create": 1, "tenant.status": 2, "user.create": 1, "membership.grant": 1}
	got := map[string]int{}
	for _, row := range c.journal() {
		if row.source != "seam" {
			continue
		}
		got[row.action]++
		if !strings.Contains(row.purpose, "command line") {
			t.Errorf("the seam row %+v does not say the write came from the command line", row)
		}
		if row.actor != "" || row.tenant != "" {
			t.Errorf("the seam row %+v carries an actor or a tenant, and the command line has neither", row)
		}
	}
	for action, n := range want {
		if got[action] != n {
			t.Errorf("the journal holds %d seam row(s) for %s, want %d; it holds %+v", got[action], action, n, c.journal())
		}
	}
}

// TestTheCommandLineTakesFlagsFromTheHostModule is the case for where the
// command-line inputs come from: a host module's schema, so the flags exist on
// the commands that take that module in and nowhere else, and the command line
// takes no positional argument.
func TestTheCommandLineTakesFlagsFromTheHostModule(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))

	help := c.run(nil, "", "tenant", "create", "--help")
	if help.err != nil || !strings.Contains(help.stdout, "--tenant-id") {
		t.Errorf("tenant create --help did not list --tenant-id (%v):\n%s", help.err, help.combined())
	}
	// migrate reaches the module for its declarations alone, so the flag is
	// refused as a key nothing on that command reads — not merely as a failed
	// run, which a migration going wrong for any other reason would also be.
	if res := c.run(nil, "", "migrate", "--tenant-id", "t_1"); res.err == nil ||
		!strings.Contains(res.stderr, config.ErrUnknownKey.Error()) || !strings.Contains(res.stderr, "--tenant-id") {
		t.Errorf("migrate accepted --tenant-id, a flag of a module it does not run, or refused it for another reason (%v):\n%s",
			res.err, res.combined())
	}
	if res := c.run(nil, "", "tenant", "create", "acme"); res.err == nil ||
		!strings.Contains(res.stderr, "acme") {
		t.Errorf("tenant create accepted a positional argument, or refused it without naming it (%v):\n%s",
			res.err, res.combined())
	}
}

// TestACommandGroupRunsNoEventBackend is the case for what the command groups
// leave out. A one-shot command on a transport bus pays for the bus on every
// run, so the groups run none; the journal takes the bus up only when its event
// list is not empty, and a configuration that fills the list therefore stops a
// command-line write at startup, naming the journal's list, rather than running
// it with a bus nobody chose for it.
func TestACommandGroupRunsNoEventBackend(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	if res := c.run(nil, "", "tenant", "list"); res.err != nil {
		t.Fatalf("tenant list failed with the journal's event list empty: %v\n%s", res.err, res.combined())
	}
	// The variable's name is spelled out, as in the case below.
	res := c.run([]string{"CRM_AUDIT__JOURNAL__EVENTS=crm.probe"}, "", "tenant", "list")
	if res.err == nil || !strings.Contains(res.stderr, journal.ModuleName) || !strings.Contains(res.stderr, "names event types") {
		t.Errorf("tenant list with an event for the journal to capture did not stop naming the journal's event list (%v):\n%s",
			res.err, res.combined())
	}
}

// TestTheEnvironmentOverridesTheFile is the case for the layers of
// configuration. The file and the environment name different database files
// for the same input item, and only the environment's is created.
func TestTheEnvironmentOverridesTheFile(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	fromEnv := filepath.Join(t.TempDir(), "from-env.db")

	// The variable's name is spelled out rather than built from envPrefix: a
	// case that assembled it the way the host does would follow the host's
	// own mistake.
	if res := c.run([]string{"CRM_DB__SQLITE__DSN=" + fromEnv}, "", "migrate"); res.err != nil {
		t.Fatalf("migrate with an overriding environment failed: %v\n%s", res.err, res.combined())
	}
	if _, err := os.Stat(fromEnv); err != nil {
		t.Errorf("the file the environment named was not created: %v", err)
	}
	if _, err := os.Stat(c.sqlitePath); err == nil {
		t.Errorf("the file the configuration named was created as well: the environment did not win")
	}
}

// TestTheOperationsEndpointMergesEveryFragment is the case for the API
// document. Every fragment a module of this binary declares is read here from
// the registry, and each of its operations has to be in the served document,
// marked with the endpoint the fragment is for.
func TestTheOperationsEndpointMergesEveryFragment(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()

	resp := h.get(t, opsEndpoint, "/openapi.json", "", noCredential{})
	expect(t, "GET /openapi.json", resp, nethttp.StatusOK)
	var served struct {
		OpenAPI   string                                           `json:"openapi"`
		Paths     map[string]map[string]map[string]json.RawMessage `json:"paths"`
		Fragments []fragmentSource                                 `json:"x-fragments"`
	}
	resp.decode(t, &served)

	specs := core.Resources[speedhttp.Spec](core.ProcessRegistry)
	if len(specs) == 0 {
		t.Fatal("no module of this binary declares an OpenAPI fragment, so this case checks nothing")
	}
	for _, spec := range specs {
		source := fragmentSource{Module: spec.Module, Endpoint: spec.Value.Endpoint}
		if !slices.Contains(served.Fragments, source) {
			t.Errorf("the document does not list the fragment %+v among %+v", source, served.Fragments)
		}
		var part fragment
		if err := json.Unmarshal(spec.Value.Document, &part); err != nil {
			t.Fatalf("reading the fragment of %s: %v", spec.Module, err)
		}
		for path, item := range part.Paths {
			for method := range item {
				operation, ok := served.Paths[path][method]
				if !ok {
					t.Errorf("the document has no %s %s, which %s declares", strings.ToUpper(method), path, spec.Module)
					continue
				}
				var endpoint string
				if err := json.Unmarshal(operation[endpointKey], &endpoint); err != nil || endpoint != spec.Value.Endpoint {
					t.Errorf("%s %s is marked for endpoint %q, want %q", strings.ToUpper(method), path, endpoint,
						spec.Value.Endpoint)
				}
			}
		}
	}
	for _, a := range applications {
		for _, res := range a.module.Resources {
			if spec, ok := res.(speedhttp.Spec); ok &&
				!slices.Contains(served.Fragments, fragmentSource{Module: a.module.Name, Endpoint: spec.Endpoint}) {
				t.Errorf("the document lists no fragment of %s for %s", a.module.Name, spec.Endpoint)
			}
		}
	}
}

// TestTheServedMemberSchemasMatchTheHandlers is the case for the fields the
// members fragments declare in the document the operations endpoint serves: a
// client generates its reader from that document, so a schema naming a field the
// handler does not send is a broken client waiting to happen. The listing and the
// grant name the account by account_id, GET /me answers an account object, and
// the listing is refused a stranger by the admission layer with the same 404 as
// an unknown tenant, not 403.
func TestTheServedMemberSchemasMatchTheHandlers(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()

	resp := h.get(t, opsEndpoint, "/openapi.json", "", noCredential{})
	expect(t, "GET /openapi.json", resp, nethttp.StatusOK)
	var doc struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	resp.decode(t, &doc)

	propertiesOf := func(raw json.RawMessage, what string) map[string]json.RawMessage {
		t.Helper()
		var node struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatalf("reading the properties of %s: %v", what, err)
		}
		return node.Properties
	}
	has := func(props map[string]json.RawMessage, field string) bool {
		_, ok := props[field]
		return ok
	}

	// The grant and the listing items name the account by account_id.
	grant := propertiesOf(doc.Components.Schemas["Membership"], "the Membership schema")
	if !has(grant, "account_id") || has(grant, "user_id") {
		t.Errorf("the Membership schema declares %v, want account_id and not user_id", schemaFields(grant))
	}
	var list struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(doc.Components.Schemas["MemberList"], &list); err != nil {
		t.Fatalf("reading the MemberList schema: %v", err)
	}
	var members struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(list.Properties["members"], &members); err != nil {
		t.Fatalf("reading the MemberList members schema: %v", err)
	}
	item := propertiesOf(members.Items, "the MemberList items schema")
	if !has(item, "account_id") || has(item, "user_id") {
		t.Errorf("the MemberList items schema declares %v, want account_id and not user_id", schemaFields(item))
	}

	// GET /me answers an account object.
	var me struct {
		Responses map[string]struct {
			Content map[string]struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"content"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(doc.Paths["/me"]["get"], &me); err != nil {
		t.Fatalf("reading the GET /me operation: %v", err)
	}
	meSchema := propertiesOf(me.Responses["200"].Content["application/json"].Schema, "the GET /me schema")
	if !has(meSchema, "account") || has(meSchema, "user") {
		t.Errorf("the GET /me schema declares %v, want account and not user", schemaFields(meSchema))
	}

	// The listing is refused a stranger with 404, not 403.
	var listing struct {
		Responses map[string]json.RawMessage `json:"responses"`
	}
	if err := json.Unmarshal(doc.Paths["/members"]["get"], &listing); err != nil {
		t.Fatalf("reading the GET /members operation: %v", err)
	}
	if _, ok := listing.Responses["403"]; ok {
		t.Errorf("the GET /members operation declares a 403, which the handler never answers: a stranger is refused 404 by the admission layer")
	}
	if _, ok := listing.Responses["404"]; !ok {
		t.Errorf("the GET /members operation declares no 404, which the admission layer answers a stranger")
	}
}

// schemaFields lists the field names of a schema's properties, for a failure
// message.
func schemaFields(props map[string]json.RawMessage) []string {
	out := make([]string, 0, len(props))
	for name := range props {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// TestTheAdmissionLayerSitsInsideTheJournalAndTheLimiter is the case for where
// the admission layer lands on the API endpoint's chain: it declares the
// authorizing stage, which the stage order puts inside the identifying, the
// throttling and the recording stages, and the chain listing is where that is
// read off.
//
// The chain the real modules assembled is also the chain-order summary the
// design leaves as an unverified assumption: every stage's modules are present
// on this endpoint, and the listing is checked for the whole order the stages
// fix — the gateway, then resolution, then rate limiting, then the journal,
// then admission — rather than the admission layer's position alone.
func TestTheAdmissionLayerSitsInsideTheJournalAndTheLimiter(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()

	rec, ok := findRecord(h.output(), "an HTTP endpoint's chain is assembled", "endpoint", apiEndpoint)
	if !ok {
		t.Fatalf("the startup diagnostics name no chain for %q; the run wrote:\n%s", apiEndpoint, h.outputForReport())
	}
	layers := chainLayers(rec["chain"])
	at := func(name string) int { return slices.Index(layers, name) }
	admission := at(admissionModuleName)
	if admission < 0 {
		t.Fatalf("the chain %q carries no %s layer", rec["chain"], admissionModuleName)
	}
	for _, outer := range []string{"authn.gate", resolve.ModuleName, journal.ModuleName, ratelimit.ModuleName} {
		switch position := at(outer); {
		case position < 0:
			t.Errorf("the chain %q is missing the %s layer the admission layer sits inside", rec["chain"], outer)
		case position > admission:
			t.Errorf("the chain %q puts %s outside %s; its refusals would not pass through it",
				rec["chain"], admissionModuleName, outer)
		}
	}
	// The whole order the stages fix, outermost first. Each layer stands
	// outside the next; a pair where one is missing is left to the loop above,
	// which names it.
	order := []string{"authn.gate", resolve.ModuleName, ratelimit.ModuleName, journal.ModuleName, admissionModuleName}
	for i := 1; i < len(order); i++ {
		outer, inner := at(order[i-1]), at(order[i])
		if outer < 0 || inner < 0 {
			continue
		}
		if outer >= inner {
			t.Errorf("the chain %q does not put %s outside %s", rec["chain"], order[i-1], order[i])
		}
	}
}

// chainLayers splits a chain listing into its layers' names, outermost first:
// "http:base -> http:fallback -> authn.gate(github.com/...) -> ..." reads as
// ["http:base", "http:fallback", "authn.gate", ...]. The listing prefixes the
// first layer of a stage with the stage marker, joined by a space, so
// "[identify] authn.gate" reads as "authn.gate".
func chainLayers(chain string) []string {
	var names []string
	for part := range strings.SplitSeq(chain, " -> ") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), "(")
		if strings.HasPrefix(name, "[") {
			_, rest, found := strings.Cut(name, "] ")
			if found {
				name = rest
			}
		}
		names = append(names, name)
	}
	return names
}

// TestTheWorkerRunsWithoutTheEntryPoint is the case for the worker. It runs the
// platform modules and no HTTP entry point; on the serving configuration as it
// stands it stops at startup, because the journal's endpoint list is the
// serving configuration's and a journal with endpoints to capture takes the
// entry point up, and with both endpoint lists emptied through the environment
// it runs and stops cleanly.
func TestTheWorkerRunsWithoutTheEntryPoint(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		res := c.run(nil, "", workerCommand)
		if res.err == nil {
			t.Fatalf("the worker started on the serving configuration's endpoint lists:\n%s", res.combined())
		}
		if !strings.Contains(res.stderr, journal.ModuleName) || !strings.Contains(res.stderr, "names endpoints") {
			t.Errorf("the worker's startup failure (%v) does not name the journal's endpoint list:\n%s",
				res.err, res.combined())
		}
		c.worker().stop(t)
	})
}

// TestAnInterruptStopsEveryStack is the case for the lifecycle's exit: SIGTERM
// ends the serving command with exit status 0 on every stack, and every
// application module the serving command runs writes its release records on
// the way out.
func TestAnInterruptStopsEveryStack(t *testing.T) {
	t.Parallel()
	var serving []string
	for _, a := range applications {
		if a.module.New != nil && slices.Contains(a.runsOn, serveCommand) {
			serving = append(serving, a.module.Name)
		}
	}
	if len(serving) == 0 {
		t.Fatal("no application module with a product runs on serve, so this case checks nothing")
	}
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()
		expect(t, "GET /ready", h.get(t, opsEndpoint, "/ready", "", noCredential{}), nethttp.StatusOK)
		h.stop(t)
		out := h.output()
		for _, name := range serving {
			for _, msg := range []string{stopMsg, closeMsg} {
				if _, ok := findRecord(out, msg, "module", name); !ok {
					t.Errorf("%s wrote no %q record as the run stopped", name, msg)
				}
			}
		}
	})
}

// The pauses the drain case takes. A request reaches its handler once its
// headers are read and the chain has run, which takes the process a few
// milliseconds; the modules' Stop stages follow the stopped record by as little.
// Each pause is far longer than that and far shorter than the entry point's
// drain.
var (
	handlerReachPause = scaledForRace(200 * time.Millisecond)
	stopStagesPause   = scaledForRace(200 * time.Millisecond)
)

// heldRequest is a request on the API endpoint whose body is sent in two
// halves, the second when the case says, so the handler waits in reading it.
type heldRequest struct {
	what     string
	rest     string
	writer   *io.PipeWriter
	answered chan outcome
}

// holdRequest sends a request's headers and the first half of its body.
func holdRequest(t *testing.T, h *crmHost, what, method, path string, ref tenantRef, cred credential, body string) *heldRequest {
	t.Helper()
	reader, writer := io.Pipe()
	req, err := nethttp.NewRequestWithContext(context.Background(), method, h.url(apiEndpoint)+path, reader)
	if err != nil {
		t.Fatalf("building %s: %v", what, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(tenantHeader, ref.Alias)
	if err := h.authorize(cred, req); err != nil {
		t.Fatalf("authorizing %s: %v", what, err)
	}
	held := &heldRequest{what: what, rest: body[len(body)/2:], writer: writer, answered: make(chan outcome, 1)}
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			held.answered <- outcome{err: err}
			return
		}
		defer resp.Body.Close()
		read, err := io.ReadAll(resp.Body)
		held.answered <- outcome{resp: response{status: resp.StatusCode, header: resp.Header, body: read}, err: err}
	}()
	if _, err := io.WriteString(writer, body[:len(body)/2]); err != nil {
		t.Fatalf("sending the first half of %s: %v", what, err)
	}
	return held
}

// answeredAt reads when the journal recorded the latest request a tenant made
// on a route: the time its layer stamped the row, once the chain inside it had
// returned.
func (c *crmCase) answeredAt(ref tenantRef, route string) (time.Time, bool) {
	c.t.Helper()
	var at time.Time
	method, path := splitRoute(route)
	err := c.sql().QueryRow(c.rebind("SELECT occurred_at FROM audit_entries "+
		"WHERE source = 'request' AND tenant_id = ? AND action = ? AND object = ? ORDER BY id DESC LIMIT 1"),
		ref.ID, method, path).Scan(&at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, false
	case err != nil:
		c.t.Fatalf("reading when %s's request on %s was recorded: %v", ref.ID, route, err)
	}
	return at, true
}

// finish sends the rest of the body and returns the answer.
func (r *heldRequest) finish(t *testing.T) response {
	t.Helper()
	if _, err := io.WriteString(r.writer, r.rest); err != nil {
		t.Errorf("sending the rest of %s: %v", r.what, err)
	}
	_ = r.writer.Close()
	select {
	case got := <-r.answered:
		if got.err != nil {
			t.Fatalf("%s was not answered: %v", r.what, got.err)
		}
		return got.resp
	case <-time.After(client.Timeout + time.Second):
		t.Fatalf("%s was not answered within the client's timeout", r.what)
	}
	return response{}
}

// TestAnInterruptDrainsRequestsInFlight is the case for the drain, on every
// stack: a request the serving process is answering when SIGTERM arrives is
// answered in full, and every module it reaches still takes its work until it
// has been. Two requests are in flight at the signal. A deal creation writes
// the deal and the journal's row for the request, and stages the deal's
// announcement in the creation's own transaction, which the outbox's relay
// publishes or leaves staged for the next relay. A settings write goes through
// the settings module and states the change through the audit seam. Then the
// process leaves with status 0.
//
// Each body is held back so that its handler waits in reading it when the
// signal arrives, and is let go once the shutdown is under way: the command's
// action writes its stopped record as it returns, the Stop stages begin, and
// the rest of each body arrives well after them. The registry stops every
// entry point before any other module, and the entry point's Stop waits for
// the requests it let in, so no application module is stopped before both
// are answered: each one's stopped record is written after the journal
// stamped the later request's row, which it does once that request's handler
// has returned, wherever the module was constructed. Under an order that
// stopped a module constructed after the entry point first, that module's
// record would come before, which is where a module that refuses work in its
// Stop refuses a request still being answered.
func TestAnInterruptDrainsRequestsInFlight(t *testing.T) {
	t.Parallel()
	var serving []string
	for _, a := range applications {
		if a.module.New != nil && slices.Contains(a.runsOn, serveCommand) {
			serving = append(serving, a.module.Name)
		}
	}
	forEachStack(t, func(t *testing.T, c *crmCase) {
		w := startDeals(t, c, nil)
		definePipeline(t, w.h, w.acme, w.asAlice, pipelineOf("acme"))
		creation := holdRequest(t, w.h, "the creation sent across the stop", nethttp.MethodPost, "/deals",
			w.acme, w.asAlice, `{"id":"in-flight","account_id":"acc-1","title":"sent across the stop","amount":10,`+
				`"currency":"EUR","stage":"qualify"}`)
		setting := holdRequest(t, w.h, "the settings write sent across the stop", nethttp.MethodPut,
			"/settings/deals.default-currency", w.acme, w.asAlice, `{"value":"CHF"}`)
		time.Sleep(handlerReachPause)

		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			w.h.stop(t)
		}()
		if !eventually(stopLimit, func() bool {
			_, ok := findRecord(w.h.output(), stoppedMsg, "command", serveCommand)
			return ok
		}) {
			t.Fatalf("the serving process wrote no %q record within %s of SIGTERM", stoppedMsg, stopLimit)
		}
		time.Sleep(stopStagesPause)
		created := creation.finish(t)
		written := setting.finish(t)
		<-stopped

		// The journal stamps a request's row once the chain inside it has
		// returned, on the serving process's clock, so the later of the two
		// stamps is when the last request in flight was answered.
		var answered time.Time
		for _, route := range []string{createDealRoute, writeSettingRoute} {
			at, ok := c.answeredAt(w.acme, route)
			if !ok {
				t.Errorf("the journal holds no row for %s answered across the stop", route)
				continue
			}
			if at.After(answered) {
				answered = at
			}
		}
		for _, name := range serving {
			rec, ok := findRecord(w.h.output(), stopMsg, "module", name)
			if !ok {
				t.Errorf("%s wrote no %q record as the run stopped", name, stopMsg)
				continue
			}
			// A record's time is written to the millisecond, cut short.
			at, err := time.Parse(time.RFC3339Nano, rec["time"])
			switch {
			case err != nil:
				t.Errorf("the %q record of %s carries the time %q: %v", stopMsg, name, rec["time"], err)
			case at.Before(answered.Truncate(time.Millisecond)):
				t.Errorf("%s was stopped at %s, before the requests in flight were answered at %s",
					name, at.Format(time.RFC3339Nano), answered.Format(time.RFC3339Nano))
			}
		}

		if created.status != nethttp.StatusCreated {
			t.Errorf("the creation sent across the stop answered %d %s, want 201", created.status, created.body)
		}
		if _, ok := c.dealRow(w.acme, "in-flight"); !ok {
			t.Errorf("acme holds no in-flight deal after its creation was sent across the stop")
		}
		// The relay may publish the creation's announcement before its own
		// stop or leave it for the next relay; the case holds that the run
		// ends with no refusal of it recorded. The announcement's own
		// transaction boundary is held by
		// TestADealWhoseAnnouncementCannotBeStagedIsNotWritten.
		if rec, ok := findRecord(w.h.output(), relayRefusedMsg, "type", "deal.created"); ok {
			t.Errorf("the bus refused the announcement of the deal created across the stop: %v", rec["err"])
		}
		if written.status != nethttp.StatusOK {
			t.Errorf("the settings write sent across the stop answered %d %s, want 200", written.status, written.body)
		}
		if got, ok := c.settingRow(w.acme.ID, "deals.default-currency"); !ok || got != "CHF" {
			t.Errorf("acme's default currency row holds %q (present %v) after the write sent across the stop, want CHF",
				got, ok)
		}
		counts := map[string]int{}
		for _, r := range c.journal() {
			switch {
			case r.requestOn(createDealRoute) && r.tenant == w.acme.ID && r.outcome == "201":
				counts["the creation's request row"]++
			case r.requestOn(writeSettingRoute) && r.tenant == w.acme.ID && r.outcome == "200":
				counts["the settings write's request row"]++
			case r.source == "seam" && r.action == settingWriteAction && r.object == "settings/deals.default-currency":
				counts["the settings write's seam row"]++
			}
		}
		for _, what := range []string{
			"the creation's request row", "the settings write's request row", "the settings write's seam row",
		} {
			if counts[what] != 1 {
				t.Errorf("the journal holds %d of %s, want 1", counts[what], what)
			}
		}
	})
}

// TestVersionAndHashAssembleNothing is the case for the two root commands
// running with no configuration source and no module: neither is given one,
// and both answer.
func TestVersionAndHashAssembleNothing(t *testing.T) {
	t.Parallel()
	version := runCommand(t, []string{"version"}, nil, "")
	if version.err != nil || strings.TrimSpace(version.stdout) != hostVersion {
		t.Errorf("version answered %q (%v), want %q", version.stdout, version.err, hostVersion)
	}
	hash := runCommand(t, []string{"hash"}, nil, operatorPassword+"\n")
	if hash.err != nil || strings.TrimSpace(hash.stdout) == "" {
		t.Errorf("hash answered %q (%v), want a hash", hash.stdout, hash.err)
	}
}

// TestAnOperatorSignsInWithWhatHashWrote is the case for what hash writes: the
// subject table's entry for the operator is the command's output, with the
// password given on standard input the way an operator gives it, and the
// operator signs in on the admin endpoint with that password. A hash of the
// right shape and the wrong contents — the line ending hashed with the
// password, say — parses and never matches, which the sign-in shows and a
// parse of the output would not. A wrong password is refused, so the sign-in
// is the entry matching rather than the table accepting anything.
func TestAnOperatorSignsInWithWhatHashWrote(t *testing.T) {
	t.Parallel()
	hash := runCommand(t, []string{"hash"}, nil, operatorPassword+"\n")
	if hash.err != nil {
		t.Fatalf("hash failed: %v\n%s", hash.err, hash.combined())
	}
	c := newCase(t, stackNamed(t, localStack))
	c.config.set("authn.static.subjects", map[string]string{operatorSubject: strings.TrimSpace(hash.stdout)})
	c.migrate()
	h := c.serve()

	expect(t, "the operator whose entry hash wrote", h.get(t, adminEndpoint, "/tenants", "", operator()), nethttp.StatusOK)
	expect(t, "the operator with another password",
		h.get(t, adminEndpoint, "/tenants", "", basicCredential{user: operatorSubject, password: "not " + operatorPassword}),
		nethttp.StatusUnauthorized)
}

// registerAs registers a tenant on the admin endpoint as the operator and
// returns the answer, whatever it is.
func (h *crmHost) registerAs(t *testing.T, id, alias string) response {
	t.Helper()
	return h.post(t, adminEndpoint, "/tenants", "", operator(), map[string]string{"id": id, "alias": alias})
}

// TestARegistrationUnderATakenAliasIsAConflict is the case for the alias half of
// a tenant's identity: it is unique on the roster, and a registration under an
// alias another tenant holds is answered as a conflict, names the refusal, and
// leaves the roster with the tenant that holds it and without the one refused.
func TestARegistrationUnderATakenAliasIsAConflict(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()
	acme := c.tenantNamed("acme")
	h.adminCreateTenant(t, acme)
	intruder := c.tenantNamed("intruder")
	resp := h.registerAs(t, intruder.ID, acme.Alias)
	expect(t, "registering another tenant under acme's alias", resp, nethttp.StatusConflict)
	var body apiError
	resp.decode(t, &body)
	if !strings.Contains(body.Error, "alias") {
		t.Errorf("the conflict says %q, which does not name the alias", body.Error)
	}
	if n := c.count("SELECT count(*) FROM tenants WHERE alias = ?", acme.Alias); n != 1 {
		t.Errorf("the roster holds %d tenants under acme's alias, want 1", n)
	}
	if n := c.count("SELECT count(*) FROM tenants WHERE id = ?", intruder.ID); n != 0 {
		t.Errorf("the roster holds the tenant refused for acme's alias")
	}
}

// TestTwoRegistrationsAtOnceAreOneSuccessAndOneConflict is the case for two
// replicas registering at once, on the stacks several replicas share: two
// tenants under one alias, and one identifier under two aliases. The roster's
// table decides either way: one registration is answered 201 and the other
// 409, and the roster holds one tenant under the alias, and one row for the
// identifier carrying the alias of the registration that succeeded. Neither
// replica's snapshot holds the tenant when the two are sent, so a registration
// that asked the snapshot whether the identifier is free would answer both 201
// and leave the later one's alias on the row. The conflict is answered with the
// body a third registration of the same kind, made once the first is written,
// is answered with: a conflict a constraint found carries the driver's error
// under the roster's, which no answer shows.
func TestTwoRegistrationsAtOnceAreOneSuccessAndOneConflict(t *testing.T) {
	t.Parallel()
	forEachSharedStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		c.config.set("tenant.catalog.refresh-period", "1s")
		hosts := c.serveReplicas(2, firstThenRest)
		registerAtOnce := func(t *testing.T, what string, bodies [2]map[string]string) [2]response {
			t.Helper()
			var answers [2]outcome
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i, body := range bodies {
				wg.Go(func() {
					<-start
					answers[i].resp, answers[i].err = hosts[i].do(request{
						method: nethttp.MethodPost, endpoint: adminEndpoint, path: "/tenants", cred: operator(), body: body,
					})
				})
			}
			close(start)
			wg.Wait()
			var got [2]response
			var statuses []int
			for i, a := range answers {
				if a.err != nil {
					t.Fatalf("the registration through replica %d could not be sent: %v", i, a.err)
				}
				got[i] = a.resp
				statuses = append(statuses, a.resp.status)
			}
			slices.Sort(statuses)
			if !slices.Equal(statuses, []int{nethttp.StatusCreated, nethttp.StatusConflict}) {
				t.Errorf("two registrations %s through two replicas answered %d %s and %d %s, want one 201 and one 409",
					what, got[0].status, got[0].body, got[1].status, got[1].body)
			}
			return got
		}
		// sameConflict holds the conflict of two registrations at once to the
		// body a later registration of the same kind is answered with.
		sameConflict := func(t *testing.T, what string, got [2]response, later map[string]string) {
			t.Helper()
			third := hosts[0].registerAs(t, later["id"], later["alias"])
			expect(t, "a later registration "+what, third, nethttp.StatusConflict)
			for _, resp := range got {
				if resp.status == nethttp.StatusConflict && string(resp.body) != string(third.body) {
					t.Errorf("the conflict of two registrations %s at once answered %s, and a later one answers %s",
						what, resp.body, third.body)
				}
			}
		}

		t.Run("under one alias", func(t *testing.T) {
			sub := c.in(t)
			first, second, third := sub.tenantNamed("first"), sub.tenantNamed("second"), sub.tenantNamed("third")
			got := registerAtOnce(t, "under one alias", [2]map[string]string{
				{"id": first.ID, "alias": first.Alias}, {"id": second.ID, "alias": first.Alias},
			})
			if n := sub.count("SELECT count(*) FROM tenants WHERE alias = ?", first.Alias); n != 1 {
				t.Errorf("the roster holds %d tenants under the one alias, want 1", n)
			}
			sameConflict(t, "under one alias", got, map[string]string{"id": third.ID, "alias": first.Alias})
		})

		t.Run("of one identifier", func(t *testing.T) {
			sub := c.in(t)
			one, other, third := sub.tenantNamed("one"), sub.tenantNamed("other"), sub.tenantNamed("third")
			bodies := [2]map[string]string{{"id": one.ID, "alias": one.Alias}, {"id": one.ID, "alias": other.Alias}}
			got := registerAtOnce(t, "of one identifier", bodies)
			sameConflict(t, "of one identifier", got, map[string]string{"id": one.ID, "alias": third.Alias})
			if n := sub.count("SELECT count(*) FROM tenants WHERE id = ?", one.ID); n != 1 {
				t.Fatalf("the roster holds %d rows for the one identifier, want 1", n)
			}
			for i, resp := range got {
				if resp.status != nethttp.StatusCreated {
					continue
				}
				if alias, _ := sub.rosterRow(one.ID); alias != bodies[i]["alias"] {
					t.Errorf("the identifier's row carries the alias %q, want %q, the one the registration answered "+
						"201 wrote", alias, bodies[i]["alias"])
				}
			}
		})
	})
}

// getOnHost reads a path on the API endpoint as the host given, with the extra
// headers given, whatever address the connection goes to.
func (h *crmHost) getOnHost(t *testing.T, host, path string, cred credential, extra headers) response {
	t.Helper()
	req, err := nethttp.NewRequestWithContext(context.Background(), nethttp.MethodGet, h.url(apiEndpoint)+path, nil)
	if err != nil {
		t.Fatalf("building a request for %s: %v", host, err)
	}
	req.Host = host
	for name, value := range extra {
		req.Header.Set(name, value)
	}
	if authErr := h.authorize(cred, req); authErr != nil {
		t.Fatalf("authorizing GET %s as %s: %v", path, host, authErr)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s as %s: %v", path, host, err)
	}
	defer resp.Body.Close()
	read, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the answer to GET %s as %s: %v", path, host, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: read}
}

// TestTheSubdomainNamesTheTenant is the case for resolution by the first label
// of the request's host, on the local stack. The label names the tenant by its
// alias in whatever case it is written and with the port dropped, so each
// tenant's own spellings reach it; a label no tenant holds is answered as an
// unknown alias is; and the header the other cases name the tenant by is not
// read, so a request carrying acme's alias there and no tenant's label in its
// host reaches no tenant.
func TestTheSubdomainNamesTheTenant(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.config.set("tenant.resolve.extractor", "subdomain")
	c.config.set("tenant.resolve.subdomain-index", 0)
	c.migrate()
	h := c.serve()
	acme, globex := c.tenantNamed("acme"), c.tenantNamed("globex")
	ann := h.adminCreateUser(t, c.userNamed("ann"))
	for _, ref := range []tenantRef{acme, globex} {
		h.adminCreateTenant(t, ref)
		h.adminGrant(t, ref, ann, roleOwner)
	}
	for _, tc := range []struct {
		host string
		want tenantRef
	}{
		{acme.Alias + ".crm.test", acme},
		{strings.ToUpper(acme.Alias) + ".CRM.test", acme},
		{acme.Alias + ".crm.test:8443", acme},
		{globex.Alias + ".crm.test", globex},
	} {
		resp := h.getOnHost(t, tc.host, "/members", signedIn(ann), nil)
		expect(t, "listing the members as "+tc.host, resp, nethttp.StatusOK)
		var got struct {
			Tenant string `json:"tenant"`
		}
		resp.decode(t, &got)
		if got.Tenant != tc.want.ID {
			t.Errorf("a request as %s reached the tenant %q, want %s", tc.host, got.Tenant, tc.want.ID)
		}
	}
	expect(t, "a request whose label no tenant holds",
		h.getOnHost(t, c.tenantNamed("nosuch").Alias+".crm.test", "/members", signedIn(ann), nil), nethttp.StatusNotFound)
	expect(t, "a request naming acme in the header alone",
		h.getOnHost(t, "crm.test", "/members", signedIn(ann), headers{tenantHeader: acme.Alias}), nethttp.StatusNotFound)
}

// requestDurationBounds are the bucket bounds of the request duration, in
// seconds, as the telemetry module fixes them.
var requestDurationBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// standardMethods are the methods the request duration carries by name; any
// other is folded into _OTHER.
var standardMethods = []string{
	nethttp.MethodGet, nethttp.MethodHead, nethttp.MethodPost, nethttp.MethodPut, nethttp.MethodPatch,
	nethttp.MethodDelete, nethttp.MethodConnect, nethttp.MethodOptions, nethttp.MethodTrace,
}

// exportedHistogramPoint is one data point of a histogram as the stdout export
// backend writes it.
type exportedHistogramPoint struct {
	Attributes []struct {
		Key   string `json:"Key"`
		Value struct {
			Value any `json:"Value"`
		} `json:"Value"`
	} `json:"Attributes"`
	Count  uint64    `json:"Count"`
	Bounds []float64 `json:"Bounds"`
}

// exportedMetric is one metric as the stdout export backend writes it.
type exportedMetric struct {
	Name string `json:"Name"`
	Unit string `json:"Unit"`
	Data struct {
		DataPoints []exportedHistogramPoint `json:"DataPoints"`
	} `json:"Data"`
}

// lastExportedMetric reads, out of a run's standard output, the last export of
// one metric: the metrics are cumulative, so the last export holds every
// request the run measured.
func lastExportedMetric(out, name string) (exportedMetric, bool) {
	var found exportedMetric
	var ok bool
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"ScopeMetrics"`) {
			continue
		}
		var export struct {
			ScopeMetrics []struct {
				Metrics []exportedMetric `json:"Metrics"`
			} `json:"ScopeMetrics"`
		}
		if json.Unmarshal([]byte(line), &export) != nil {
			continue
		}
		for _, scope := range export.ScopeMetrics {
			for _, m := range scope.Metrics {
				if m.Name == name {
					found, ok = m, true
				}
			}
		}
	}
	return found, ok
}

// TestTheRequestDurationKeepsItsFixedShape is the case for the request
// duration histogram, whose name, unit, bucket bounds and attributes are a
// fixed public surface, on the local stack, whose metrics leave through
// standard output. Requests on the API endpoint are measured under their
// method and status alone — no route and no tenant — and a method outside the
// standard nine is folded into _OTHER rather than carried by name.
func TestTheRequestDurationKeepsItsFixedShape(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()
	acme := c.tenantNamed("acme")
	ann := h.adminCreateUser(t, c.userNamed("ann"))
	h.adminCreateTenant(t, acme)
	h.adminGrant(t, acme, ann, roleOwner)
	token := bearer(h.login(t, ann))
	for range 3 {
		expect(t, "listing acme's members", h.get(t, apiEndpoint, "/members", acme.Alias, token), nethttp.StatusOK)
	}
	purge := h.send(t, request{method: "PURGE", endpoint: apiEndpoint, path: "/members", tenant: acme.Alias, cred: token})
	if purge.status < 400 {
		t.Fatalf("PURGE /members answered %d, want a refusal", purge.status)
	}
	h.stop(t)

	metric, ok := lastExportedMetric(h.output(), "http.server.request.duration")
	if !ok {
		t.Fatalf("the run exported no http.server.request.duration; it wrote:\n%s", h.outputForReport())
	}
	if metric.Unit != "s" {
		t.Errorf("the request duration is exported in %q, want s", metric.Unit)
	}
	var gets, others uint64
	for _, point := range metric.Data.DataPoints {
		if !slices.Equal(point.Bounds, requestDurationBounds) {
			t.Errorf("a data point of the request duration has the bounds %v, want %v", point.Bounds, requestDurationBounds)
		}
		values := map[string]any{}
		for _, attr := range point.Attributes {
			values[attr.Key] = attr.Value.Value
		}
		keys := slices.Sorted(maps.Keys(values))
		if err := requestDurationAttributeError(keys); err != nil {
			t.Errorf("a data point of the request duration %v", err)
			continue
		}
		method, _ := values["http.request.method"].(string)
		if method != "_OTHER" && !slices.Contains(standardMethods, method) {
			t.Errorf("a data point of the request duration carries the method %q by name", method)
		}
		switch status, _ := values["http.response.status_code"].(float64); {
		case method == nethttp.MethodGet && status == nethttp.StatusOK:
			gets += point.Count
		case method == "_OTHER" && int(status) == purge.status:
			others += point.Count
		}
	}
	if gets < 3 {
		t.Errorf("the request duration counts %d GETs answered 200, want at least the 3 listings", gets)
	}
	if others != 1 {
		t.Errorf("the request duration counts %d requests of another method answered %d, want the one PURGE", others,
			purge.status)
	}
}

// TestABodyPastTheEndpointsLimitIsRefused is the case for the entry point's
// body limit, on the local stack: a contact whose body is past the API
// endpoint's max-body-bytes is refused as too large and writes nothing, and
// one within it is written.
func TestABodyPastTheEndpointsLimitIsRefused(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.sealWithCaseKey()
	c.setOnEndpoint(apiEndpoint, "max-body-bytes", 4096)
	c.migrate()
	h := c.serve()
	acme := c.tenantNamed("acme")
	ann := h.adminCreateUser(t, c.userNamed("ann"))
	h.adminCreateTenant(t, acme)
	h.adminGrant(t, acme, ann, roleOwner)
	token := bearer(h.login(t, ann))
	expect(t, "opening acc-1", h.send(t, api(nethttp.MethodPost, "/accounts", acme, token,
		map[string]any{"id": "acc-1", "name": "Rocket"})), nethttp.StatusCreated)
	contact := func(id, name string) map[string]any {
		return map[string]any{"id": id, "account_id": "acc-1", "name": name, "email": id + "@rocket.test"}
	}
	expect(t, "a contact past the body limit",
		h.send(t, api(nethttp.MethodPost, "/contacts", acme, token, contact("con-big", strings.Repeat("x", 8192)))),
		nethttp.StatusRequestEntityTooLarge)
	if n := c.count("SELECT count(*) FROM contacts WHERE tenant_id = ? AND id = ?", acme.ID, "con-big"); n != 0 {
		t.Errorf("acme holds the contact whose body was past the limit")
	}
	expect(t, "a contact within the body limit",
		h.send(t, api(nethttp.MethodPost, "/contacts", acme, token, contact("con-1", "Ann"))), nethttp.StatusCreated)
}

// TestARequestNoRouteServesIsRefusedAsAProblem is the case for the answers the
// HTTP module writes itself, on the local stack: a path no route serves and a
// method the path does not take. Both are problem details, and both pass
// through the layers first, so on a guarded endpoint the gateway answers a
// request with no credential before the module answers that its path has no
// route, and the Allow header the module sets for a method the path does not
// take reaches no caller a layer turned away. A HEAD request is a method a path
// serving GET alone does not take.
func TestARequestNoRouteServesIsRefusedAsAProblem(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()
	acme := c.tenantNamed("acme")
	ann := h.adminCreateUser(t, c.userNamed("ann"))
	h.adminCreateTenant(t, acme)
	h.adminGrant(t, acme, ann, roleOwner)

	unrouted := h.get(t, opsEndpoint, "/nosuch", "", noCredential{})
	expect(t, "a path no route serves on the unguarded endpoint", unrouted, nethttp.StatusNotFound)
	requireProblem(t, "the answer to a path no route serves", unrouted)

	wrongMethod := h.send(t, request{method: nethttp.MethodPost, endpoint: opsEndpoint, path: "/health"})
	expect(t, "a method the health path does not take", wrongMethod, nethttp.StatusMethodNotAllowed)
	requireProblem(t, "the answer to a method the path does not take", wrongMethod)
	if allow := wrongMethod.header.Get("Allow"); allow != nethttp.MethodGet {
		t.Errorf("the answer to POST /health allows %q, want %s", allow, nethttp.MethodGet)
	}
	// The router sends a HEAD request only to a route registered for HEAD, so
	// a path that serves GET alone does not take it.
	head := h.send(t, request{method: nethttp.MethodHead, endpoint: opsEndpoint, path: "/health"})
	expect(t, "a HEAD request to the health path", head, nethttp.StatusMethodNotAllowed)

	member := h.get(t, apiEndpoint, "/nosuch", acme.Alias, signedIn(ann))
	expect(t, "a member asking for a path no route serves", member, nethttp.StatusNotFound)
	requireProblem(t, "the answer to a member asking for a path no route serves", member)

	deleteMembers := h.send(t, request{
		method: nethttp.MethodDelete, endpoint: apiEndpoint, path: "/members", tenant: acme.Alias, cred: signedIn(ann),
	})
	expect(t, "a member deleting the member list", deleteMembers, nethttp.StatusMethodNotAllowed)
	requireProblem(t, "the answer to a member using a method the members path does not take", deleteMembers)
	if allow := deleteMembers.header.Get("Allow"); allow != "POST, GET" {
		t.Errorf("the answer to DELETE /members allows %q, want \"POST, GET\"", allow)
	}

	anonymous := h.get(t, apiEndpoint, "/nosuch", acme.Alias, noCredential{})
	expect(t, "no credential asking for a path no route serves", anonymous, nethttp.StatusUnauthorized)
	requireProblem(t, "the gateway's answer to a path no route serves", anonymous)

	anonymousDelete := h.send(t, request{
		method: nethttp.MethodDelete, endpoint: apiEndpoint, path: "/members", tenant: acme.Alias, cred: noCredential{},
	})
	expect(t, "no credential using a method the members path does not take", anonymousDelete, nethttp.StatusUnauthorized)
	requireProblem(t, "the gateway's answer to a method the members path does not take", anonymousDelete)
	if allow := anonymousDelete.header.Get("Allow"); allow != "" {
		t.Errorf("the gateway's refusal of DELETE /members carries Allow %q, which tells a caller with no credential "+
			"which methods the path takes", allow)
	}
}

// TestAnInProcessBackendRefusesADeploymentOfSeveralReplicas is the case for the
// deployment fact the in-process backends judge, on the local stack: a
// configuration that says several replicas share the key-value store or the
// bus stops the serving command at startup, naming the item, since each
// replica would hold its own and every call would still succeed.
func TestAnInProcessBackendRefusesADeploymentOfSeveralReplicas(t *testing.T) {
	t.Parallel()
	for _, item := range []string{"kv.memory.replicas", "event.memory.replicas"} {
		t.Run(item, func(t *testing.T) {
			c := newCase(t, stackNamed(t, localStack))
			c.migrate()
			c.config.set(item, 2)
			res := c.run(nil, "", serveCommand)
			if res.err == nil {
				t.Fatalf("the serving command ran with %s at 2:\n%s", item, res.combined())
			}
			if !strings.Contains(res.stderr, item) {
				t.Errorf("the serving command's startup failure (%v) does not name %s:\n%s", res.err, item, res.combined())
			}
		})
	}
}

// TestAJSONConfigurationReachesTheSameItems is the case for the other format and
// the other locator, on the local stack: the configuration written as JSON
// reaches the same items as the YAML one, whether its locator is given with
// --config or through CRM_CONFIG. A tenant registered through the one is
// listed through the other, from the same database.
func TestAJSONConfigurationReachesTheSameItems(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	c.oneShotConfig()
	c.mu.Lock()
	body, err := json.Marshal(c.config)
	c.mu.Unlock()
	if err != nil {
		t.Fatalf("encoding the configuration as JSON: %v", err)
	}
	path := filepath.Join(c.dir, "config-one-shot.json")
	if err := writeFileAtomic(path, body); err != nil {
		t.Fatalf("writing the JSON configuration: %v", err)
	}
	locator := "file://" + path

	acme := c.tenantNamed("acme")
	created := runCommand(t, []string{
		c.stack.name, "tenant", "create", "--tenant-id", acme.ID,
		"--tenant-alias", acme.Alias, "--config", locator,
	}, nil, "")
	if created.err != nil {
		t.Fatalf("tenant create on the JSON configuration failed: %v\n%s", created.err, created.combined())
	}
	listed := runCommand(t, []string{c.stack.name, "tenant", "list"}, []string{"CRM_CONFIG=" + locator}, "")
	if listed.err != nil {
		t.Fatalf("tenant list on the JSON configuration through CRM_CONFIG failed: %v\n%s", listed.err, listed.combined())
	}
	var roster []tenantView
	c.resultOf(listed, &roster)
	if !slices.ContainsFunc(roster, func(v tenantView) bool { return v.ID == acme.ID && v.Alias == acme.Alias }) {
		t.Errorf("tenant list through CRM_CONFIG answered %+v, want the tenant created through --config", roster)
	}
}

// The factor cases drive the multi-step factor flows the identity system's
// modules serve: enrolling a TOTP factor, logging in with it as a second factor,
// a step-up on a session that already has one, the guard middleware in front of
// a host route, and the admission layer's tenant-level factor requirement. They
// run on the stack that needs no docker, since the property is the identity
// system's and not a backend's.

// totpPeriod is the step the TOTP factor fixes its codes at, written here so a
// case can derive a code for a named instant rather than wait for the clock.
const totpPeriod = 30 * time.Second

// totpCode derives the code a TOTP seed produces at an instant, from the otpauth
// address the enrollment answered. The address carries the seed and the
// parameters the factor generated it with, so the code is the one the factor's
// own generator makes.
func totpCode(t *testing.T, otpauthURI string, at time.Time) string {
	t.Helper()
	key, err := otp.NewKeyFromURL(otpauthURI)
	if err != nil {
		t.Fatalf("reading the otpauth address: %v", err)
	}
	code, err := totp.GenerateCodeCustom(key.Secret(), at, totp.ValidateOpts{
		Period:    uint(key.Period()),
		Digits:    key.Digits(),
		Algorithm: key.Algorithm(),
	})
	if err != nil {
		t.Fatalf("deriving a TOTP code: %v", err)
	}
	return code
}

// flowReply is a step of a login flow as a client reads it: the step number the
// next answer carries, the state, and what the flow asks for next.
type flowReply struct {
	Seq   int    `json:"seq"`
	State string `json:"state"`
	Next  struct {
		Kind    string `json:"kind"`
		Options struct {
			OTPAuthURI string `json:"otpauth_uri"`
		} `json:"options"`
		Factors []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"factors"`
	} `json:"next"`
}

// beginAuthenticatedFlow starts a link, step-up or enroll flow on a session
// cookie and returns the flow cookie the reply set and the opening step.
func (h *crmHost) beginAuthenticatedFlow(t *testing.T, sessionCookie, purpose string, input map[string]string) (string, flowReply) {
	t.Helper()
	resp := h.send(t, request{
		method: nethttp.MethodPost, endpoint: accountEndpoint, path: "/auth/flows/authenticated",
		header: map[string]string{"Cookie": sessionCookieName + "=" + sessionCookie},
		body:   map[string]any{"purpose": purpose, "input": input},
	})
	if resp.status != nethttp.StatusOK {
		t.Fatalf("beginning the %s flow answered %d: %s", purpose, resp.status, resp.body)
	}
	var reply flowReply
	resp.decode(t, &reply)
	cookie, ok := cookieOf(resp.header, flowCookieName)
	if !ok {
		t.Fatalf("beginning the %s flow set no %s cookie", purpose, flowCookieName)
	}
	return cookie, reply
}

// answerFlow answers one step of a cookie-bound flow and returns the reply
// together with the response, so a caller reads the session cookie off the
// completion.
func (h *crmHost) answerFlow(t *testing.T, flowCookie string, seq int, step string, input map[string]string) (flowReply, response) {
	t.Helper()
	body := map[string]any{"seq": seq, "step": step}
	if input != nil {
		body["input"] = input
	}
	resp := h.send(t, request{
		method: nethttp.MethodPost, endpoint: accountEndpoint, path: loginAnswerPath,
		header: map[string]string{"Cookie": flowCookieName + "=" + flowCookie},
		body:   body,
	})
	if resp.status != nethttp.StatusOK {
		t.Fatalf("answering step %q (seq %d) answered %d: %s", step, seq, resp.status, resp.body)
	}
	var reply flowReply
	resp.decode(t, &reply)
	return reply, resp
}

// enrollTOTP registers a TOTP factor for the session's account and returns the
// otpauth address the enrollment answered, which is where the next login's code
// is derived from. The factor is active once the confirmation code is accepted,
// which is the last registration step.
func (h *crmHost) enrollTOTP(t *testing.T, sessionCookie string) string {
	t.Helper()
	flowCookie, begun := h.beginAuthenticatedFlow(t, sessionCookie, "enroll", map[string]string{"kind": "totp"})
	uri := begun.Next.Options.OTPAuthURI
	if begun.Next.Kind == "" || uri == "" {
		t.Fatalf("beginning a TOTP enrollment answered next %+v, want the otpauth address", begun.Next)
	}
	// The confirmation code is the current step's, what an authenticator app
	// would show now.
	code := totpCode(t, uri, time.Now())
	done, _ := h.answerFlow(t, flowCookie, begun.Seq, "enroll", map[string]string{"code": code})
	if done.Next.Kind != "complete" {
		t.Fatalf("the TOTP confirmation answered next %q, want complete", done.Next.Kind)
	}
	return uri
}

// loginWithTOTP runs a password login through to a session, answering the TOTP
// second factor the account now requires, and returns the session cookie. The
// start carries the password, so the first-factor step is the start's own reply;
// the account has an active factor, so the flow lands on the factor step.
func (h *crmHost) loginWithTOTP(t *testing.T, u userRef, uri string) string {
	t.Helper()
	start := h.send(t, request{
		method: nethttp.MethodPost, endpoint: accountEndpoint, path: loginFlowPath,
		body: map[string]any{
			"method": "password", "delivery": "session",
			"input": map[string]string{"identifier": u.Email, "password": u.Password},
		},
	})
	expect(t, "starting a password login for "+u.Email+" with a factor enrolled", start, nethttp.StatusOK)
	var begun flowReply
	start.decode(t, &begun)
	if begun.Next.Kind != "factor" || len(begun.Next.Factors) == 0 {
		t.Fatalf("a password login for %s answered next %+v, want a factor step", u.Email, begun.Next)
	}
	flowCookie, ok := cookieOf(start.header, flowCookieName)
	if !ok {
		t.Fatalf("starting a password login for %s set no %s cookie", u.Email, flowCookieName)
	}
	factorID := begun.Next.Factors[0].ID
	challenge, _ := h.answerFlow(t, flowCookie, begun.Seq, "factor", map[string]string{"factor": factorID})
	if challenge.Next.Kind != "challenge" {
		t.Fatalf("challenging the factor answered next %q, want challenge", challenge.Next.Kind)
	}
	// The enrollment spent the current step's counter, so this login presents the
	// next step's code: it is above the counter the enrollment consumed and
	// within the one step either side of now the factor accepts.
	code := totpCode(t, uri, time.Now().Add(totpPeriod))
	verified, _ := h.answerFlow(t, flowCookie, challenge.Seq, "factor",
		map[string]string{"factor": factorID, "code": code})
	if verified.Next.Kind != "complete" {
		t.Fatalf("verifying the second factor answered next %q, want complete", verified.Next.Kind)
	}
	done, resp := h.answerFlow(t, flowCookie, verified.Seq, "complete", nil)
	if done.State != "authenticated" {
		t.Fatalf("completing the login answered state %q, want authenticated", done.State)
	}
	cookie, ok := cookieOf(resp.header, sessionCookieName)
	if !ok {
		t.Fatalf("completing the login set no %s cookie", sessionCookieName)
	}
	return cookie
}

// TestAFactorGatedRouteAdmitsOnlyAMultiFactorSession is the case for the guard
// middleware in front of a host route: the route asking a multi-factor identity
// answers a single-factor session with the RFC 9470 step-up 401 and its
// challenge, and lets a session that completed a second factor through. The
// session that enrolled the factor is single-factor still, so it is refused; a
// fresh login that answers the factor is multi-factor, so it is admitted with
// the level the route asked for.
func TestAFactorGatedRouteAdmitsOnlyAMultiFactorSession(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	acme := c.createTenant("acme")
	ann := c.createUser("ann")
	c.grant(acme, ann, roleOwner)
	h := c.serve()

	_, gatePath := splitRoute(factorGateRoute)
	single := h.login(t, ann)
	before := h.get(t, accountEndpoint, gatePath, "", bearer(single))
	expect(t, "a single-factor session at the factor-gated route", before, nethttp.StatusUnauthorized)
	requireStepUpChallenge(t, "the factor-gated route", before, true)
	var problem struct {
		Type string `json:"type"`
	}
	before.decode(t, &problem)
	if problem.Type != "urn:speed:authn:insufficient_user_authentication" {
		t.Errorf("the refusal's type is %q, want urn:speed:authn:insufficient_user_authentication", problem.Type)
	}

	uri := h.enrollTOTP(t, single)
	multi := h.loginWithTOTP(t, ann, uri)
	after := h.get(t, accountEndpoint, gatePath, "", bearer(multi))
	expect(t, "a multi-factor session at the factor-gated route", after, nethttp.StatusOK)
	var summary struct {
		Account   string   `json:"account"`
		Assurance int      `json:"assurance"`
		Methods   []string `json:"methods"`
	}
	after.decode(t, &summary)
	if summary.Account != string(ann.ID) || summary.Assurance != 2 {
		t.Errorf("the factor-gated route answered %+v, want account %s at assurance 2", summary, ann.ID)
	}
	if !slices.Contains(summary.Methods, "otp") {
		t.Errorf("the summary's methods are %v, want the second factor otp among them", summary.Methods)
	}
}

// TestAStepUpCannotStrengthenTheSessionItEnrolledOn is the case for the rule
// that a factor registered during an authentication may not strengthen it: a
// step-up on the session that enrolled the factor is refused with the step-up
// challenge, because the factor was activated after the session authenticated.
// The rule is what keeps a password holder who registers their own factor from
// turning that session into a multi-factor one before the real owner notices.
func TestAStepUpCannotStrengthenTheSessionItEnrolledOn(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	acme := c.createTenant("acme")
	ann := c.createUser("ann")
	c.grant(acme, ann, roleOwner)
	h := c.serve()

	session := h.login(t, ann)
	h.enrollTOTP(t, session)

	start := h.send(t, request{
		method: nethttp.MethodPost, endpoint: accountEndpoint, path: "/auth/flows/authenticated",
		header: map[string]string{"Cookie": sessionCookieName + "=" + session},
		body:   map[string]any{"purpose": "step-up"},
	})
	expect(t, "a step-up on the session that enrolled the factor", start, nethttp.StatusUnauthorized)
	requireStepUpChallenge(t, "the step-up", start, true)
}

// TestATenantRequiringMFARefusesASingleFactorMember is the case for the
// admission layer's tenant-level factor requirement: in a tenant that requires
// multi-factor, a member whose session is single-factor is answered the step-up
// 401 before its request reaches a handler, and the same member reaches a tenant
// that requires nothing. The requirement is per tenant, so the two tenants
// answer the same request differently.
func TestATenantRequiringMFARefusesASingleFactorMember(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	acme := c.createTenant("acme")
	globex := c.createTenant("globex")
	ann := c.createUser("ann")
	c.grant(acme, ann, roleOwner)
	c.grant(globex, ann, roleOwner)
	// acme requires its members to have authenticated with more than one kind of
	// factor. The setting is the admission layer's, written into acme's layer
	// before the run reads its snapshot.
	c.setForTenant(acme, "tenant.admission.require-mfa", "true")
	h := c.serve()

	session := h.login(t, ann)
	refused := h.get(t, apiEndpoint, "/members", acme.Alias, bearer(session))
	expect(t, "a single-factor member in a tenant requiring multi-factor", refused, nethttp.StatusUnauthorized)
	requireStepUpChallenge(t, "the admission layer", refused, false)
	admitted := h.get(t, apiEndpoint, "/members", globex.Alias, bearer(session))
	expect(t, "the same member in a tenant requiring nothing", admitted, nethttp.StatusOK)
}

// requireStepUpChallenge fails the case unless a refusal carries the RFC 9470
// step-up challenge: the error code and the multi-factor level, and, when
// withMaxAge is set, the freshness window the requirement asked for. The guard
// writes max_age when its requirement is fresh; the admission layer compares a
// level alone and writes none.
func requireStepUpChallenge(t *testing.T, what string, got response, withMaxAge bool) {
	t.Helper()
	challenge := got.header.Get("WWW-Authenticate")
	parts := []string{`error="insufficient_user_authentication"`, `acr_values="2"`}
	if withMaxAge {
		parts = append(parts, `max_age="600"`)
	}
	for _, part := range parts {
		if !strings.Contains(challenge, part) {
			t.Errorf("the %s challenge %q does not carry %s", what, challenge, part)
		}
	}
}
