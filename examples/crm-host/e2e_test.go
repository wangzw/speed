package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	nethttp "net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
// The memberships table references the users table, and its migration is
// applied after the users one because the members module requires the users
// module's table token; the migration would fail on PostgreSQL otherwise, since
// "members" sorts before "users". A membership naming no user is refused
// through the locator the host reads the database with, on SQLite as on
// PostgreSQL.
func TestMigrateBuildsEveryDeclaredTable(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		c.requireTables("migrate")

		_, err := c.sql().Exec(c.rebind("INSERT INTO memberships (tenant_id, user_id, role, granted_at) VALUES (?, ?, ?, ?)"),
			c.tenantNamed("acme").ID, "u_nobody", roleRep, time.Now().UTC())
		if err == nil {
			t.Errorf("a membership naming no user was written: the reference to the users table is not enforced")
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
// the token reaches the API; the Basic credential reaches the same route and is
// answered for the same user under the other backend's name; the account
// endpoint answers who the caller is across tenants; the second user sees their
// own tenant; the owner grants from the API; and a logout ends the token.
func TestEveryStackServesTheSamePath(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		globex := c.createTenant("globex")
		alice := c.createUser("alice")
		bob := c.createUser("bob")
		c.grant(acme, alice, roleOwner)
		c.grant(globex, bob, roleRep)
		h := c.serve()

		// The login route is exempt, so a credential that does not sign
		// anyone in is answered by the login handler, with its error body,
		// rather than by the gateway, whose refusals carry none. The frozen
		// exemption table the gateway lists at startup names the route.
		wrong := h.post(t, accountEndpoint, "/login", "", basicCredential{user: alice.Email, password: "not it"}, nil)
		expect(t, "a login with the wrong password", wrong, nethttp.StatusUnauthorized)
		var refusal apiError
		wrong.decode(t, &refusal)
		if refusal.Error == "" {
			t.Errorf("the login's refusal carries no error text, so the gateway answered it: %q", wrong.body)
		}
		if _, ok := findRecord(h.output(), "authn: the exemption table, frozen", accountEndpoint, loginRoute); !ok {
			t.Errorf("the frozen exemption table does not list %s on %s; the run wrote:\n%s",
				loginRoute, accountEndpoint, h.outputForReport())
		}

		token := h.login(t, alice)

		viaToken := h.members(t, acme.Alias, bearer(token))
		// The context carries the identifier, never the alias the request
		// named: renaming a tenant rewrites no row.
		if viaToken.Tenant != acme.ID {
			t.Errorf("the listing ran in tenant %q, want the identifier %q behind the alias %q",
				viaToken.Tenant, acme.ID, acme.Alias)
		}
		if want := sessionsBackendName + "/" + alice.ID; viaToken.Caller != want {
			t.Errorf("a bearer request was answered for %q, want %q", viaToken.Caller, want)
		}
		if viaToken.Role != roleOwner {
			t.Errorf("the caller's role reads %q, want %q", viaToken.Role, roleOwner)
		}
		requireMembers(t, viaToken, alice)

		viaBasic := h.members(t, acme.Alias, basic(alice))
		if want := usersBackendName + "/" + alice.ID; viaBasic.Caller != want {
			t.Errorf("a Basic request was answered for %q, want %q", viaBasic.Caller, want)
		}
		requireMembers(t, viaBasic, alice)

		me := h.me(t, bearer(token))
		if me.User == nil || me.User.ID != alice.ID || me.User.Email != alice.Email {
			t.Errorf("GET /me answered user %+v, want %s (%s)", me.User, alice.ID, alice.Email)
		}
		wantTenants := []meTenant{{ID: acme.ID, Alias: acme.Alias, Status: "active", Role: roleOwner}}
		if !slices.Equal(me.Tenants, wantTenants) {
			t.Errorf("GET /me answered the tenants %+v, want %+v", me.Tenants, wantTenants)
		}

		bobs := h.members(t, globex.Alias, basic(bob))
		if bobs.Tenant != globex.ID || bobs.Role != roleRep {
			t.Errorf("bob's listing ran in %q as %q, want %q as %q", bobs.Tenant, bobs.Role, globex.ID, roleRep)
		}
		requireMembers(t, bobs, bob)

		// A subtest of its own, so that what it finds does not end the
		// rest of the path.
		t.Run("the owner grants from the API, and a second grant rewrites the role", func(t *testing.T) {
			// The row the grant writes names no tenant: the filter fills
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
				if got.TenantID != acme.ID || got.UserID != bob.ID || got.Role != role {
					t.Errorf("the grant answered %+v, want %s in %s as %s", got, bob.ID, acme.ID, role)
				}
			}
			inAcme := h.members(t, acme.Alias, basic(bob))
			requireMembers(t, inAcme, alice, bob)
			if inAcme.Role != roleViewer {
				t.Errorf("bob's role in acme reads %q after the second grant, want %q", inAcme.Role, roleViewer)
			}
			// The grant stayed in acme: bob's membership in globex is the
			// one the command line wrote, and globex still holds bob alone.
			inGlobex := h.members(t, globex.Alias, basic(bob))
			requireMembers(t, inGlobex, bob)
			if inGlobex.Role != roleRep {
				t.Errorf("bob's role in globex reads %q after the grants in acme, want the %q the command line wrote",
					inGlobex.Role, roleRep)
			}
			expect(t, "bob, not an owner or an admin of acme, granting there",
				h.post(t, apiEndpoint, "/members", acme.Alias, basic(bob),
					map[string]string{"email": alice.Email, "role": roleViewer}), nethttp.StatusForbidden)
		})

		requireSpanExported(t, c, h)

		expect(t, "a logout with the token", h.post(t, accountEndpoint, "/logout", "", bearer(token), nil),
			nethttp.StatusNoContent)
		expect(t, "the token after its logout", h.get(t, accountEndpoint, "/me", "", bearer(token)),
			nethttp.StatusUnauthorized)
		requireRefusalCause(t, h, sessionsBackendName, errUnknownSession)
	})
}

// requireRefusalCause waits for the gateway's record of a refusal by one
// backend and holds its cause to the text the backend refused with. The record
// is how an operator learns why a credential was turned away; a cause that
// reaches it masked tells them nothing.
func requireRefusalCause(t *testing.T, h *crmHost, backend string, cause error) {
	t.Helper()
	var causes []string
	if !eventually(settleLimit, func() bool {
		causes = causes[:0]
		for _, rec := range records(h.output()) {
			if rec["attributed-to"] == backend && rec["outcome"] == "denied" {
				causes = append(causes, rec["cause"])
				if rec["cause"] == cause.Error() {
					return true
				}
			}
		}
		return false
	}) {
		t.Errorf("no refusal by %s is recorded with the cause %q; the refusals it made are recorded with %q",
			backend, cause.Error(), causes)
	}
}

// requireSpanExported waits for the API requests' server span to leave through
// the export backend the stack names: the in-process receiver for the OTLP
// backend, which decodes it, the run's standard output for the stdout one. The
// span is named for the endpoint and the method the telemetry layer observed,
// which is a string nothing else in the run writes.
func requireSpanExported(t *testing.T, c *crmCase, h *crmHost) {
	t.Helper()
	if c.receiver != nil {
		name := apiEndpoint + " " + nethttp.MethodGet
		if !eventually(settleLimit, func() bool { return len(c.receiver.spansNamed(name)) > 0 }) {
			t.Errorf("no span named %s reached the OTLP receiver after the requests were served", name)
		}
		c.receiver.requireDecoded(t)
		return
	}
	spanName := `"Name":"` + apiEndpoint + ` GET"`
	if !eventually(settleLimit, func() bool { return strings.Contains(h.output(), spanName) }) {
		t.Errorf("no span named %s reached the run's standard output; it wrote:\n%s", spanName, h.outputForReport())
	}
}

// sessionLifetime is the lifetime the session case writes, and lifetimeSlack
// how far either side of it the refusal may land. The slack covers the clocks
// the backends judge an expiry by — the store's own, or the writer's and the
// reader's — and a second-granular expiry.
const (
	sessionLifetime = 3 * time.Second
	lifetimeSlack   = time.Second
)

// TestASessionEndsWhenItsLifetimeLapses is the case for the session lifetime:
// the setting the sessions module declares, read from the settings surface at
// login, and honoured by the key-value backend the stack names.
//
// The lifetime is written as a global row before the run starts, the login
// answers with it, and the token is honoured at once and refused once the
// lifetime has passed — neither never, which is a store keeping it for ever,
// nor well before, which is a store reading the lifetime in another unit.
func TestASessionEndsWhenItsLifetimeLapses(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		alice := c.createUser("alice")
		c.setGlobal(ttlSetting, sessionLifetime.String())
		h := c.serve()

		issuedAt := time.Now()
		resp := h.post(t, accountEndpoint, "/login", "", basic(alice), nil)
		expect(t, "a login", resp, nethttp.StatusCreated)
		var got issued
		resp.decode(t, &got)
		if got.ExpiresIn != int64(sessionLifetime/time.Second) {
			t.Errorf("the login answered expires_in %d, want the %s %s holds", got.ExpiresIn, sessionLifetime, ttlSetting)
		}
		expect(t, "the token at once", h.get(t, accountEndpoint, "/me", "", bearer(got.Token)), nethttp.StatusOK)

		// Polled on the account endpoint, which the rate limiter does not
		// guard, and no faster than a person would retry.
		const every = 100 * time.Millisecond
		var refusedAt time.Time
		for time.Since(issuedAt) < sessionLifetime+5*lifetimeSlack {
			if h.get(t, accountEndpoint, "/me", "", bearer(got.Token)).status == nethttp.StatusUnauthorized {
				refusedAt = time.Now()
				break
			}
			time.Sleep(every)
		}
		switch lived := refusedAt.Sub(issuedAt); {
		case refusedAt.IsZero():
			t.Errorf("the token was still honoured %s after a login whose lifetime is %s",
				time.Since(issuedAt).Round(time.Millisecond), sessionLifetime)
		case lived < sessionLifetime-lifetimeSlack:
			t.Errorf("the token was refused %s after the login, before its lifetime of %s had passed",
				lived.Round(time.Millisecond), sessionLifetime)
		}
	})
}

// TestALifetimeThatIsNotPositiveIssuesNoSession is the case for the lifetime a
// store cannot keep. The key-value capability stores a value whose lifetime is
// not positive with no expiry at all, so a login that passed such a lifetime on
// would issue a token that never ends; the login refuses instead, and the
// record it writes names the setting.
func TestALifetimeThatIsNotPositiveIssuesNoSession(t *testing.T) {
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	alice := c.createUser("alice")
	c.setGlobal(ttlSetting, (0 * time.Second).String())
	h := c.serve()

	resp := h.post(t, accountEndpoint, "/login", "", basic(alice), nil)
	expect(t, "a login while "+ttlSetting+" is zero", resp, nethttp.StatusInternalServerError)
	if !eventually(settleLimit, func() bool {
		_, ok := findRecord(h.output(), "the session lifetime is not positive, so no session is issued",
			"setting", ttlSetting)
		return ok
	}) {
		t.Errorf("no record names %s as the reason the login was refused; the run wrote:\n%s",
			ttlSetting, h.outputForReport())
	}
}

// TestTwoReplicasShareTheirSessions is the case for replicas on shared
// backends. The two are started together against one database and one
// key-value store, as a rollout starts them; a token one replica issues is
// honoured by the other, because the session lives in the store and not in the
// process, and a logout on the second ends the session on the first.
func TestTwoReplicasShareTheirSessions(t *testing.T) {
	forEachSharedStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		alice := c.createUser("alice")
		c.grant(acme, alice, roleRep)
		replicas := c.serveReplicas(2, together)
		first, second := replicas[0], replicas[1]

		token := first.login(t, alice)
		list := second.members(t, acme.Alias, bearer(token))
		if want := sessionsBackendName + "/" + alice.ID; list.Caller != want {
			t.Errorf("the second replica answered a token the first issued for %q, want %q", list.Caller, want)
		}
		expect(t, "a logout on the second replica",
			second.post(t, accountEndpoint, "/logout", "", bearer(token), nil), nethttp.StatusNoContent)
		expect(t, "the token on the first replica after the logout on the second",
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
		ids = append(ids, m.UserID)
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
// caller can reach it: the membership layer, the gateway before resolution, the
// two tenant refusals being one answer, the admin endpoint's two audiences,
// and a disabled user.
func TestTheRequestSurfaceKeepsTenantsApart(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		acme := c.createTenant("acme")
		globex := c.createTenant("globex")
		initech := c.createTenant("initech")
		c.disableTenant(initech)
		alice := c.createUser("alice")
		c.grant(acme, alice, roleOwner)
		h := c.serve()

		t.Run("a member naming another tenant is refused, and the refusal is journalled", func(t *testing.T) {
			sub := c.in(t)
			resp := h.get(t, apiEndpoint, "/members", globex.Alias, basic(alice))
			expect(t, "alice naming globex", resp, nethttp.StatusForbidden)
			var body apiError
			resp.decode(t, &body)
			if body.Error == "" {
				t.Errorf("the refusal carries no error text: %q", resp.body)
			}
			// The layer sits inside the journal, so its refusal is a request
			// row: the route, the status, the tenant the request named and
			// the caller who named it.
			sub.awaitJournal("the membership layer's refusal", func(r auditRow) bool {
				return r.source == "request" && r.object == listMembersRoute && r.outcome == "403" &&
					r.tenant == globex.ID && r.actor == usersBackendName+"/"+alice.ID
			})
		})

		t.Run("a request with no credential is refused before resolution", func(t *testing.T) {
			sub := c.in(t)
			unknown := sub.tenantNamed("nosuch").Alias
			expect(t, "no credential naming an unknown tenant",
				h.get(t, apiEndpoint, "/members", unknown, noCredential{}), nethttp.StatusUnauthorized)
			// The same tenant with a credential is resolution's to refuse,
			// which is what tells the two layers apart by the answer: a
			// gateway inside resolution would have answered 404 above.
			expect(t, "a credential naming an unknown tenant",
				h.get(t, apiEndpoint, "/members", unknown, basic(alice)), nethttp.StatusNotFound)
		})

		t.Run("a disabled tenant and an unknown one get the same answer", func(t *testing.T) {
			sub := c.in(t)
			disabled := h.get(t, apiEndpoint, "/members", initech.Alias, basic(alice))
			unknown := h.get(t, apiEndpoint, "/members", sub.tenantNamed("nosuch").Alias, basic(alice))
			// Asserted exactly, not merely as "the same": two answers that
			// agree because both fell through to the membership layer's 403
			// would be equal without the roster having judged anything.
			expect(t, "a request naming a disabled tenant", disabled, nethttp.StatusNotFound)
			if disabled.status != unknown.status || string(disabled.body) != string(unknown.body) {
				t.Errorf("a disabled tenant answered %d %q and an unknown one %d %q; telling them apart enumerates the roster",
					disabled.status, disabled.body, unknown.status, unknown.body)
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
			list := h.members(t, fresh.Alias, basic(carol))
			if list.Tenant != fresh.ID || list.Role != roleAdmin {
				t.Errorf("carol's listing ran in %q as %q, want %q as %q", list.Tenant, list.Role, fresh.ID, roleAdmin)
			}
			requireMembers(t, list, carol)

			// The grant crossed tenants from an endpoint that resolves none,
			// and the seam is where it says so: the operator is the actor and
			// the purpose is stated.
			seam := sub.awaitJournal("the operator's grant", func(r auditRow) bool {
				return r.source == "seam" && r.action == "membership.grant" &&
					r.actor == operatorOwner+"/"+operatorSubject && strings.Contains(r.object, fresh.ID)
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
					h.get(t, apiEndpoint, "/members", fresh.Alias, basic(carol)), step.want)
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
			expect(t, "alice's Basic credential on the admin endpoint",
				h.get(t, adminEndpoint, "/tenants", "", basic(alice)), nethttp.StatusForbidden)
			// The operator check sits inside the journal, so a CRM user
			// knocking on the admin endpoint leaves a request row with the
			// caller named.
			sub.awaitJournal("the operator check's refusal", func(r auditRow) bool {
				return r.source == "request" && r.object == listTenantsRoute && r.outcome == "403" &&
					r.actor == usersBackendName+"/"+alice.ID
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
		t.Run("a disabled user is refused whichever credential they present", func(t *testing.T) {
			sub := c.in(t)
			token := h.login(t, alice)
			expect(t, "alice before she is disabled", h.get(t, accountEndpoint, "/me", "", basic(alice)), nethttp.StatusOK)
			sub.exec("UPDATE users SET disabled = ? WHERE id = ?", true, alice.ID)
			expect(t, "a disabled user's Basic credential",
				h.get(t, accountEndpoint, "/me", "", basic(alice)), nethttp.StatusUnauthorized)
			expect(t, "a disabled user's token",
				h.get(t, accountEndpoint, "/me", "", bearer(token)), nethttp.StatusUnauthorized)
			expect(t, "a disabled user's login",
				h.post(t, accountEndpoint, "/login", "", basic(alice), nil), nethttp.StatusUnauthorized)
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
// in the driver's error alone, and no answer carries them. The derivation runs
// at the production cost here, since its duration is what holds the two
// apart: at the cases' own cost the second could find the first at its
// look-up.
func TestTwoCreationsOfOneEmailAreOneSuccessAndOneConflict(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.config.set("users.password-iterations", defaultHashIterations)
		c.migrate()
		h := c.serve()
		u := c.userNamed("twice")
		body := map[string]string{"email": u.Email, "display_name": "twice", "password": u.Password}

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

// TestAPasswordIsDerivedAtTheConfiguredCost is the case for the cost a CRM
// user's password is derived at, on the local stack: a user created at the
// command line and one created through the admin endpoint each store the
// count users.password-iterations gave when they were created, and a count
// that is not positive stops the serving command at startup, naming the item.
// Two users created at one count sign in once the deployment gives another,
// since each hash carries its own, one with a login and one with a Basic
// credential, and each sign-in stores that user's hash again at the count now
// given: the decoy a login naming no user is checked against is derived at
// that count, and a user left at the old one costs another time to sign in.
// The rewritten hash is the password's: the user signs in again with it.
func TestAPasswordIsDerivedAtTheConfiguredCost(t *testing.T) {
	const first, moved = 1_000, 2_000
	c := newCase(t, stackNamed(t, localStack))
	c.config.set("users.password-iterations", first)
	c.migrate()
	storedCount := func(u userRef) string {
		t.Helper()
		var stored string
		if err := c.sql().QueryRow(c.rebind("SELECT password_hash FROM users WHERE id = ?"), u.ID).Scan(&stored); err != nil {
			t.Fatalf("reading the password hash of %s: %v", u.Email, err)
		}
		parts := strings.Split(stored, "$")
		if len(parts) != 4 {
			t.Fatalf("the password hash of %s has %d parts, want 4", u.Email, len(parts))
		}
		return parts[1]
	}

	early := c.createUser("early")
	if got, want := storedCount(early), strconv.Itoa(first); got != want {
		t.Errorf("a user created at the command line stores the count %s, want the configured %s", got, want)
	}
	basicEarly := c.createUser("basic-early")

	c.config.set("users.password-iterations", moved)
	h := c.serve()
	h.login(t, early)
	if got, want := storedCount(early), strconv.Itoa(moved); got != want {
		t.Errorf("a user created at %d stores the count %s once signed in with a login at %d, want %s",
			first, got, moved, want)
	}
	h.login(t, early)
	expect(t, "a Basic credential of a user created at the old count",
		h.get(t, accountEndpoint, "/me", "", basic(basicEarly)), nethttp.StatusOK)
	if got, want := storedCount(basicEarly), strconv.Itoa(moved); got != want {
		t.Errorf("a user created at %d stores the count %s once signed in with a Basic credential at %d, want %s",
			first, got, moved, want)
	}
	expect(t, "a Basic credential of that user once the hash was stored again",
		h.get(t, accountEndpoint, "/me", "", basic(basicEarly)), nethttp.StatusOK)
	late := h.adminCreateUser(t, c.userNamed("late"))
	if got, want := storedCount(late), strconv.Itoa(moved); got != want {
		t.Errorf("a user created through the admin endpoint stores the count %s, want the configured %s", got, want)
	}
	h.stop(t)

	const item = "users.password-iterations"
	c.config.set(item, 0)
	res := c.run(nil, "", serveCommand)
	if res.err == nil {
		t.Fatalf("the serving command ran with %s at 0:\n%s", item, res.combined())
	}
	if !strings.Contains(res.stderr, item) {
		t.Errorf("the serving command's startup failure (%v) does not name %s:\n%s", res.err, item, res.combined())
	}
}

// The refusal the hash case adds to the users table: its name, and the display
// name it refuses.
const (
	userRefusalName    = "crm_case_refuses_users"
	refusedDisplayName = "refused by the case"
)

// TestAPasswordHashReachesNoOutput is the case for where a CRM user's password
// hash goes, on every stack: into the users table and nowhere else. The case
// has the database refuse one creation, through a refusal it adds to the users
// table once the migration has run — a CHECK on PostgreSQL, a trigger on
// SQLite, neither of which the host knows of — and the refused creation is
// answered 500 and leaves neither the hash it derived nor the address in the
// serving process's output.
//
// The database module keeps a logger on the handle it delivers, and that
// logger prints a statement that failed to standard output without the values
// bound into it, so the refused insert is printed neither with the hash nor
// with the address. The host does not route its statements around the logger
// the delivered handle carries: the claim is the module's, and this case
// holds it.
func TestAPasswordHashReachesNoOutput(t *testing.T) {
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		if c.dialect == "postgres" {
			c.exec("ALTER TABLE users ADD CONSTRAINT " + userRefusalName +
				" CHECK (display_name <> '" + refusedDisplayName + "')")
		} else {
			c.exec("CREATE TRIGGER " + userRefusalName + " BEFORE INSERT ON users WHEN NEW.display_name = '" +
				refusedDisplayName + "' BEGIN SELECT RAISE(ABORT, 'the case refuses this user'); END")
		}
		h := c.serve()
		u := c.userNamed("refused")
		resp := h.post(t, adminEndpoint, "/users", "", operator(),
			map[string]string{"email": u.Email, "display_name": refusedDisplayName, "password": u.Password})
		expect(t, "a creation the database refuses", resp, nethttp.StatusInternalServerError)
		if n := c.count("SELECT count(*) FROM users WHERE email = ?", u.Email); n != 0 {
			t.Fatalf("the users table holds %d rows for %s after the refused creation, want none", n, u.Email)
		}
		// The run's output is whole once it has stopped.
		h.stop(t)
		out := h.output()
		if _, ok := findRecord(out, "creating a user"); !ok {
			t.Fatalf("the serving process recorded no failure of the refused creation, so the refusal was never met")
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, hashScheme+"$") {
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

// unlandedMsg is the startup record of an HTTP middleware constraint that
// placed nothing.
const unlandedMsg = "an HTTP middleware constraint landed on no layer"

// TestTheMembershipLayerSitsInsideTheJournalAndTheLimiter is the case for where
// the membership layer lands on the API endpoint's chain. The layer anchors on
// the four it has to sit inside, and the chain listing is where those anchors
// are readable as positions that landed rather than as spellings: an anchor no
// layer stands for is dropped with nothing said about where the layer went.
//
// The startup's record of constraints that landed on no layer is held to the
// ones this configuration is known to leave: the journal anchors inside
// resolution on every endpoint it covers, and two of those endpoints resolve no
// tenant. Those two are reported, and at Info: no layer there stands for the
// capability, which is a legal assembly rather than a mistake to warn about.
// Any other is a layer whose position guarantee is gone — a module a slice
// added anchoring on a capability nobody stands for on that endpoint — and
// fails here by name.
func TestTheMembershipLayerSitsInsideTheJournalAndTheLimiter(t *testing.T) {
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	h := c.serve()

	rec, ok := findRecord(h.output(), "an HTTP endpoint's chain is assembled", "endpoint", apiEndpoint)
	if !ok {
		t.Fatalf("the startup diagnostics name no chain for %q; the run wrote:\n%s", apiEndpoint, h.outputForReport())
	}
	layers := chainLayers(rec["chain"])
	at := func(name string) int { return slices.Index(layers, name) }
	members := at(membersModuleName)
	if members < 0 {
		t.Fatalf("the chain %q carries no %s layer", rec["chain"], membersModuleName)
	}
	for _, outer := range []string{"authn.gate", resolve.ModuleName, journal.ModuleName, ratelimit.ModuleName} {
		switch position := at(outer); {
		case position < 0:
			t.Errorf("the chain %q is missing the %s layer the membership layer sits inside", rec["chain"], outer)
		case position > members:
			t.Errorf("the chain %q puts %s outside %s; its refusals would not pass through it",
				rec["chain"], membersModuleName, outer)
		}
	}

	resolution := reflect.TypeFor[tenant.Resolution]()
	resolutionName := resolution.PkgPath() + "." + resolution.Name()
	reported := map[string]bool{}
	for _, r := range records(h.output()) {
		if r["msg"] != unlandedMsg {
			continue
		}
		known := (r["endpoint"] == accountEndpoint || r["endpoint"] == adminEndpoint) &&
			r["middleware"] == journal.ModuleName && r["capability"] == resolutionName
		if !known {
			t.Errorf("a middleware constraint landed on no layer beyond the journal's resolution anchor on %s and %s: "+
				"endpoint=%s middleware=%s declaration=%s capability=%s cause=%q",
				accountEndpoint, adminEndpoint, r["endpoint"], r["middleware"], r["declaration"], r["capability"], r["cause"])
			continue
		}
		reported[r["endpoint"]] = true
		// No layer on those endpoints stands for resolution, which an
		// assembly resolving tenants on some endpoints alone leaves by
		// design, so the record is information rather than a warning.
		if r["level"] != slog.LevelInfo.String() {
			t.Errorf("the journal's resolution anchor on %s, which no layer there stands for, is reported at %s, "+
				"want %s", r["endpoint"], r["level"], slog.LevelInfo)
		}
	}
	for _, endpoint := range []string{accountEndpoint, adminEndpoint} {
		if !reported[endpoint] {
			t.Errorf("the startup does not report the journal's resolution anchor on %s, which resolves no tenant, "+
				"as landing on no layer; the run wrote:\n%s", endpoint, h.outputForReport())
		}
	}
}

// chainLayers splits a chain listing into its layers' names, outermost first:
// "http:base -> authn.gate(github.com/...) -> ..." reads as
// ["http:base", "authn.gate", ...].
func chainLayers(chain string) []string {
	var names []string
	for part := range strings.SplitSeq(chain, " -> ") {
		name, _, _ := strings.Cut(strings.TrimSpace(part), "(")
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
	handlerReachPause = scaledForRace(500 * time.Millisecond)
	stopStagesPause   = scaledForRace(500 * time.Millisecond)
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
	cred.apply(req)
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
	err := c.sql().QueryRow(c.rebind("SELECT occurred_at FROM audit_entries "+
		"WHERE source = 'request' AND tenant_id = ? AND object = ? ORDER BY id DESC LIMIT 1"), ref.ID, route).Scan(&at)
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
			case r.source == "request" && r.object == createDealRoute && r.tenant == w.acme.ID && r.outcome == "201":
				counts["the creation's request row"]++
			case r.source == "request" && r.object == writeSettingRoute && r.tenant == w.acme.ID && r.outcome == "200":
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
	cred.apply(req)
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
		resp := h.getOnHost(t, tc.host, "/members", basic(ann), nil)
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
		h.getOnHost(t, c.tenantNamed("nosuch").Alias+".crm.test", "/members", basic(ann), nil), nethttp.StatusNotFound)
	expect(t, "a request naming acme in the header alone",
		h.getOnHost(t, "crm.test", "/members", basic(ann), headers{tenantHeader: acme.Alias}), nethttp.StatusNotFound)
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
		if !slices.Equal(keys, []string{"http.request.method", "http.response.status_code"}) {
			t.Errorf("a data point of the request duration carries the attributes %v, want the method and the status alone",
				keys)
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

// TestAnInProcessBackendRefusesADeploymentOfSeveralReplicas is the case for the
// deployment fact the in-process backends judge, on the local stack: a
// configuration that says several replicas share the key-value store or the
// bus stops the serving command at startup, naming the item, since each
// replica would hold its own and every call would still succeed.
func TestAnInProcessBackendRefusesADeploymentOfSeveralReplicas(t *testing.T) {
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
