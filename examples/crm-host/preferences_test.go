package main

// These cases hold the settings and audit surfaces' authorization, which the
// host decides through the authorization layer now rather than through its own
// member_roles table. Two shapes are held. One drives the whole host end to end
// on a stack — a real serve, a real policy the grants write, a member signing in
// — so what a route answers is what a running deployment answers. The other
// holds the write boundary with the layer's handler-internal API over a
// stand-in engine, so the 403-or-503 split is read without a policy store.

import (
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/authz"
	authztest "github.com/vislake/speed/pkg/authz/authztest"
	authzguard "github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	speedhttp "github.com/vislake/speed/pkg/http"
)

// guardProduct drives the real authorization module to Init over a stand-in
// engine, catalog and entry point, and hands back the handler-internal API it
// delivers. The engine is the case's, so the answer the handler reads is the
// one a case chose: a denial, an outage, or a policy the case seeded.
func guardProduct(t *testing.T, enforcer authz.Enforcer) authzguard.Guard {
	t.Helper()
	guardModule := authzguard.Module()
	reader := authztest.ConfigReaderAll(t, businessGuardConfig, guardModule)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	reg.Register(guardModule)
	reg.Register(authzBusinessModule())
	authzSupply(reg, "stand-in.enforcer", (*authz.Enforcer)(nil), enforcer)
	authzSupply(reg, "stand-in.catalog", (*authz.Catalog)(nil), authztest.NewCatalog())
	authzSupply(reg, "stand-in.router", (*speedhttp.Router)(nil), authzRouterStub{})

	if err := reg.Drive(t.Context(), core.Drive{
		Depth: core.StageInit,
		Participants: []string{
			authzguard.ModuleName, "stand-in.enforcer", "stand-in.catalog", "stand-in.router",
			authzBusinessModuleName,
		},
	}); err != nil {
		t.Fatalf("driving the authorization module over a stand-in engine failed: %v", err)
	}
	guard, err := core.Resolve[authzguard.Guard](reg)
	if err != nil {
		t.Fatalf("resolving the handler-internal authorization API: %v", err)
	}
	return guard
}

// settingsWriteRequest builds the request a settings write handler runs on,
// carrying an identity and no tenant: the permission is asked before the tenant
// is read, so a refusal needs neither a roster nor a resolution layer.
func settingsWriteRequest(t *testing.T, account authn.AccountID) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(nethttp.MethodPut, "/settings/accounts.page-size", nil)
	c.Request = req.WithContext(authntest.Context(t, req.Context(), authntest.WithAccount(account)))
	return c, rec
}

// TestAPolicyDenialIsA403AndAnEngineOutageIsA503 is the case for the write
// boundary the handler-internal API draws, over a stand-in engine: a permission
// no policy row grants is a 403, and an engine that cannot answer is a 503. The
// two are held apart because a denial is a fact about a caller and an outage is
// not — reading a failure as a denial would make a recoverable outage a
// permanent permission conclusion.
func TestAPolicyDenialIsA403AndAnEngineOutageIsA503(t *testing.T) {
	t.Parallel()

	t.Run("a policy denial is a 403", func(t *testing.T) {
		// An engine with no matching row denies every query, as the real one
		// does when no grant reaches the caller.
		product := &preferencesProduct{guard: guardProduct(t, authztest.NewEnforcer())}
		c, rec := settingsWriteRequest(t, "alice")
		product.writeSetting(c)
		if rec.Code != nethttp.StatusForbidden {
			t.Fatalf("a settings write with no grant answered %d, want 403", rec.Code)
		}
	})

	t.Run("an engine outage is a 503 and not a denial", func(t *testing.T) {
		enforcer := authztest.NewEnforcer()
		enforcer.Failing(errors.New("the engine is down"))
		product := &preferencesProduct{guard: guardProduct(t, enforcer)}
		c, rec := settingsWriteRequest(t, "alice")
		product.writeSetting(c)
		if rec.Code == nethttp.StatusForbidden {
			t.Fatal("an engine outage was written as a denial")
		}
		if rec.Code == nethttp.StatusOK {
			t.Fatal("an engine outage was written as a pass")
		}
		if rec.Code != nethttp.StatusServiceUnavailable {
			t.Fatalf("an engine outage answered %d, want 503", rec.Code)
		}
	})
}

// TestTheSettingsAndTrailSurfacesAreDecidedByTheAuthorizationLayer is the case
// for the authorization the settings and audit surfaces carry, on a running
// host: the host no longer reads a role of its own table, so a member's role is
// a policy binding the grant writes and the permission a route needs is decided
// by the authorization layer.
//
// An admin holds settings.read, settings.write and audit.read; a viewer holds
// none of them. The same routes answer the two differently:
//
//   - a viewer reading the settings is refused 403;
//   - an admin reads and writes the settings;
//   - a caller without settings.write is refused before the key is looked up,
//     so unlisted platform key is a 403 for the viewer but a 404 for the admin;
//   - the trail is decided by audit.read the same way.
func TestTheSettingsAndTrailSurfacesAreDecidedByTheAuthorizationLayer(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.migrate()
		h := c.serve()
		acme, globex := c.tenantNamed("acme"), c.tenantNamed("globex")
		h.adminCreateTenant(t, acme)
		h.adminCreateTenant(t, globex)
		ann := h.adminCreateUser(t, c.userNamed("ann"))
		rex := h.adminCreateUser(t, c.userNamed("rex"))
		// ann administers acme and rex views globex. The grants write each
		// account's role into the policy, which is what the routes read now.
		h.adminGrant(t, acme, ann, roleAdmin)
		h.adminGrant(t, globex, rex, roleViewer)

		t.Run("a viewer is refused the settings read", func(t *testing.T) {
			expect(t, "a viewer listing the settings",
				h.get(t, apiEndpoint, "/settings", globex.Alias, signedIn(rex)), nethttp.StatusForbidden)
		})

		t.Run("an admin reads and writes the settings", func(t *testing.T) {
			settingsListing(t, h, apiEndpoint, "/settings", acme.Alias, signedIn(ann))
			expect(t, "an admin writing a setting",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "accounts.page-size", 40),
				nethttp.StatusOK)
		})

		t.Run("a caller without settings.write is refused before the key is looked up", func(t *testing.T) {
			// ratelimit.limit is a platform key, not one a tenant edits, so an
			// admin that holds settings.write is answered 404 for it. A viewer
			// that holds no settings.write is answered 403 first: the
			// permission is asked before the key is resolved.
			expect(t, "a viewer writing an unlisted key",
				writeTenantSettingAs(t, h, globex.Alias, signedIn(rex), "ratelimit.limit", 100000),
				nethttp.StatusForbidden)
			expect(t, "an admin writing an unlisted key",
				writeTenantSettingAs(t, h, acme.Alias, signedIn(ann), "ratelimit.limit", 100000),
				nethttp.StatusNotFound)
		})

		t.Run("the trail is decided by audit.read", func(t *testing.T) {
			expect(t, "a viewer reading the trail",
				h.get(t, apiEndpoint, "/audit", globex.Alias, signedIn(rex)), nethttp.StatusForbidden)
			readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann), nil)
		})
	})
}

// TestTheSettingsAndTrailRoutesAreDeclaredOnAGuardThatRefusesByDefault is the
// case for the route declaration the settings and audit routes carry: the host
// runs the authorization layer's default disposition, where a route nobody
// declared is refused before its handler runs, and the routes reach their
// handlers anyway because they are registered open. It is the property the
// handler-internal API depends on — an open route is what carries a request
// past the middleware to the handler that asks the permission.
func TestTheSettingsAndTrailRoutesAreDeclaredOnAGuardThatRefusesByDefault(t *testing.T) {
	t.Parallel()
	c := newCase(t, stackNamed(t, localStack))
	c.migrate()
	// Closed is the layer's default; written here so the case does not rest on
	// a host configuration that leaves it open.
	c.config.set("authz.guard.undeclared", "closed")
	h := c.serve()
	acme := c.tenantNamed("acme")
	h.adminCreateTenant(t, acme)
	ann := h.adminCreateUser(t, c.userNamed("ann"))
	h.adminGrant(t, acme, ann, roleAdmin)

	// A route the layer holds no declaration for would be a 403 here. The
	// settings listing and the trail answer the admin, which is only reachable
	// because each route is registered open.
	settingsListing(t, h, apiEndpoint, "/settings", acme.Alias, signedIn(ann))
	readTrailPage(t, h, apiEndpoint, "/audit", acme.Alias, signedIn(ann), nil)
}
