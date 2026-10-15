package main

import (
	"context"
	"log/slog"
	nethttp "net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	authzguard "github.com/vislake/speed/pkg/authz/guard"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/kv"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/settings"
	"github.com/vislake/speed/pkg/tenant"
)

// The cases of the pipeline, activities, flow and report routes' declarations:
// the routes the host mounts on the API endpoint whose least role the role
// matrix fixes. The middleware decides each route from the permission it
// declares, so a route is reached by the role that holds that permission and
// refused to the roles below it, and a route with no declaration at all is
// refused for every caller under the closed default.

// TestPipelineRoutesTakeTheirLeastRole is the case for the pipeline routes now
// that the enforcement middleware decides them. A viewer reads the stages; the
// definition and the change of a stage take the pipeline write permission, which
// the matrix grants to an admin and an owner and to no one below, so a viewer and
// a rep are refused and an admin and the owner reach them.
func TestPipelineRoutesTakeTheirLeastRole(t *testing.T) {
	t.Parallel()
	forEachStack(t, func(t *testing.T, c *crmCase) {
		c.sealWithCaseKey()
		c.migrate()
		h := c.serve()
		cr := setUpCrew(t, c, h)
		alice := cr.as(h, cr.alice, cr.acme) // owner
		ivan := cr.as(h, cr.ivan, cr.acme)   // admin
		rita := cr.as(h, cr.rita, cr.acme)   // rep
		vera := cr.as(h, cr.vera, cr.acme)   // viewer

		stage := func(key string) map[string]any {
			return map[string]any{"key": key, "name": "Stage " + key, "position": 1, "probability": 50, "kind": "open"}
		}

		// The read takes a viewer: the least member reaches it.
		vera.must(t, nethttp.MethodGet, "/pipeline/stages", nil, nethttp.StatusOK)

		// The definition takes the write permission: a viewer and a rep are refused,
		// an admin and the owner reach it.
		refused(t, "a viewer defining a stage", vera.send(t, nethttp.MethodPost, "/pipeline/stages", stage("v1")))
		refused(t, "a rep defining a stage", rita.send(t, nethttp.MethodPost, "/pipeline/stages", stage("r1")))
		ivan.must(t, nethttp.MethodPost, "/pipeline/stages", stage("a1"), nethttp.StatusCreated)
		alice.must(t, nethttp.MethodPost, "/pipeline/stages", stage("o1"), nethttp.StatusCreated)

		// The change takes the write permission too.
		refused(t, "a rep changing a stage",
			rita.send(t, nethttp.MethodPatch, "/pipeline/stages/a1", map[string]any{"probability": 60}))
		refused(t, "a viewer changing a stage",
			vera.send(t, nethttp.MethodPatch, "/pipeline/stages/a1", map[string]any{"probability": 60}))
		ivan.must(t, nethttp.MethodPatch, "/pipeline/stages/a1", map[string]any{"probability": 60}, nethttp.StatusOK)
	})
}

// The cases below hold the pipeline, activities, flow and report routes'
// declarations, the surface the enforcement middleware reads. They drive the four
// modules together with a route-declaration surface and stand-ins for every other
// capability they take, so what they check is what an assembly's startup runs:
// every route a module binds on the API endpoint declares a permission, and each
// declares the exact one the role matrix intends.

// sliceGuardConfig is the configuration these drives run on: the endpoint the
// middleware would guard, named the way the stand-in entry point declares it.
const sliceGuardConfig = `{"authz":{"guard":{"endpoints":["api"]}}}`

// sliceRouter stands for the HTTP entry point: one endpoint whose registrations
// are dropped.
type sliceRouter struct{}

func (sliceRouter) Endpoint(string) (speedhttp.Endpoint, error) { return sliceEndpoint{}, nil }
func (sliceRouter) Endpoints() []string                         { return []string{apiEndpoint} }

// sliceEndpoint accepts registrations and does nothing with them.
type sliceEndpoint struct{}

func (sliceEndpoint) Route(string, string, ...gin.HandlerFunc) {}
func (sliceEndpoint) Use(speedhttp.Middleware)                 {}
func (sliceEndpoint) Accepting() bool                          { return true }

// sliceBus is a stand-in for the event bus: the flow and reports modules
// subscribe in their Init, so Subscribe is present and does nothing, where an
// embedded nil interface would panic.
type sliceBus struct{}

func (sliceBus) Publish(context.Context, event.Event) error               { return nil }
func (sliceBus) Subscribe(eventType string, name string, h event.Handler) {}

// sliceLogger is a stand-in for the logging capability. Its redaction accepts
// every rule and does nothing, so a module that registers redaction keys at
// construction is not handed a nil.
type sliceLogger struct{}

func (sliceLogger) Named(string) *slog.Logger { return slog.New(slog.DiscardHandler) }
func (sliceLogger) Redaction() log.Redaction  { return sliceRedaction{} }

// sliceRedaction accepts every rule and does nothing with it.
type sliceRedaction struct{}

func (sliceRedaction) AddKeys(...string)              {}
func (sliceRedaction) AddPattern(string, log.Matcher) {}

// sliceSupply registers a stand-in module and supplies its instance, so a driven
// module resolves the capability from it exactly as a host's assembly hands one
// over.
func sliceSupply(reg *core.Registry, name string, token core.Token, instance any) {
	reg.Register(core.Module{Name: name, Provides: []core.Provision{{Token: token}}})
	reg.Supply(instance, name)
}

// newSliceRoutesRegistry builds a registry holding the four route-declaring
// modules of this host's pipeline and reporting side beside a route-declaration
// surface and stand-ins for every other capability they require, and names the
// modules a drive of it takes in.
//
// When guard is true the surface is the real enforcement module's over the host's
// business vocabulary and a stand-in engine and catalog, so a drive to Start holds
// every declaration these modules make to the permission catalog and to the
// resolved translation. When guard is false the surface is the given stand-in
// gate and no enforcement module runs, so a case reads back what the modules
// declared without the enforcement module's checks.
func newSliceRoutesRegistry(t *testing.T, gate authz.Gate, guard bool) (*core.Registry, []string) {
	t.Helper()
	reg := core.New()
	reader := authztest.ConfigReaderAll(t, sliceGuardConfig, authzguard.Module())
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	names := []string{
		pipelineModuleName, activitiesModuleName, flowModuleName, reportsModuleName,
		"stand-in.router", "stand-in.db", "stand-in.roster", "stand-in.kv", "stand-in.bus",
		"stand-in.settings", "stand-in.accounts", "stand-in.log",
	}
	if guard {
		reg.Register(authzguard.Module())
		reg.Register(authzBusinessModule())
		sliceSupply(reg, "stand-in.enforcer", (*authz.Enforcer)(nil), authztest.NewEnforcer())
		sliceSupply(reg, "stand-in.catalog", (*authz.Catalog)(nil), authztest.NewCatalog(
			permPipelineRead, permPipelineWrite, permActivityRead, permActivityWrite, permActivityDel,
			permFlowRead, permReportRead))
		names = append(names, authzguard.ModuleName, authzBusinessModuleName, "stand-in.enforcer", "stand-in.catalog")
	} else {
		sliceSupply(reg, "stand-in.gate", (*authz.Gate)(nil), gate)
		names = append(names, "stand-in.gate")
	}
	reg.Register(pipelineModule())
	reg.Register(activitiesModule())
	reg.Register(flowModule())
	reg.Register(reportsModule())
	sliceSupply(reg, "stand-in.router", (*speedhttp.Router)(nil), sliceRouter{})
	sliceSupply(reg, "stand-in.db", (*db.Database)(nil), struct{ db.Database }{})
	sliceSupply(reg, "stand-in.roster", (*tenant.Catalog)(nil), struct{ tenant.Catalog }{})
	sliceSupply(reg, "stand-in.kv", (*kv.KV)(nil), struct{ kv.KV }{})
	sliceSupply(reg, "stand-in.bus", (*event.Bus)(nil), sliceBus{})
	sliceSupply(reg, "stand-in.settings", (*settings.Store)(nil), struct{ settings.Store }{})
	sliceSupply(reg, "stand-in.accounts", (*authn.Accounts)(nil), struct{ authn.Accounts }{})
	sliceSupply(reg, "stand-in.log", (*log.Logger)(nil), sliceLogger{})
	return reg, names
}

// sliceRoutes is every route the four modules bind on the API endpoint, with the
// permission each declares, in registration order, so a case that holds the whole
// surface to a property names them once.
var sliceRoutes = []struct {
	route string
	perm  authz.Permission
}{
	{listStagesRoute, permPipelineRead},
	{createStageRoute, permPipelineWrite},
	{patchStageRoute, permPipelineWrite},
	{createActivityRoute, permActivityWrite},
	{deleteActivityRoute, permActivityDel},
	{dealTimelineRoute, permActivityRead},
	{flowRoute, permFlowRead},
	{pipelineReportRoute, permReportRead},
	{leaderboardRoute, permReportRead},
	{revenueRoute, permReportRead},
	{deliveriesRoute, permReportRead},
}

// TestThePipelineReportRoutesDeclareEveryRoute is the case for the four modules'
// route declarations, the surface the enforcement middleware reads.
//
// Every route these modules bind on the API endpoint declares a permission, so a
// request that reaches one is decided by the policy rather than refused as an
// undeclared route: under the closed default a route with no declaration is
// answered 403 for every caller, whatever role the caller holds. The observation
// therefore discriminates: with a `Require` dropped for a route, that route is
// undeclared and this case finds it not covered, while with the modules as
// written every one is covered. The drive runs the real enforcement module, so a
// declaration naming a permission the host does not declare, or one the resolved
// translation cannot express, fails the start rather than leaving a route that
// decides nothing at request time.
func TestThePipelineReportRoutesDeclareEveryRoute(t *testing.T) {
	t.Parallel()
	reg, names := newSliceRoutesRegistry(t, nil, true)
	err := reg.Drive(t.Context(), core.Drive{
		Depth:        core.StageStart,
		Participants: names,
		Action: func(_ context.Context, reg *core.Registry) error {
			gate, err := core.Resolve[authz.Gate](reg)
			if err != nil {
				return err
			}
			for _, r := range sliceRoutes {
				method, path := splitRoute(r.route)
				if !gate.Covered(method, path) {
					t.Errorf("the route %s carries no declaration, so the middleware refuses it 403 for every caller "+
						"under the closed default", r.route)
				}
			}
			// The control: a route the modules do not declare is not covered,
			// which is the state a dropped declaration leaves a real route in.
			if gate.Covered(nethttp.MethodGet, "/pipeline/undeclared") {
				t.Error("a route the modules do not declare reads as covered")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("driving the pipeline and reporting modules with the enforcement module failed: %v", err)
	}
}

// TestThePipelineReportRoutesNeedTheirPermissions is the case for which permission
// each route declares, and it is what makes the role matrix reach the routes: a
// viewer holds the read and export permissions, a rep adds the writes and the
// activity delete, and an admin adds the pipeline write. A route declared on the
// wrong permission would let a role the matrix does not intend reach it, so each
// route is held to the exact permission it needs.
func TestThePipelineReportRoutesNeedTheirPermissions(t *testing.T) {
	t.Parallel()
	gate := authztest.NewGate()
	reg, names := newSliceRoutesRegistry(t, gate, false)
	if err := reg.Drive(t.Context(), core.Drive{Depth: core.StageInit, Participants: names}); err != nil {
		t.Fatalf("driving the pipeline and reporting modules with a stand-in gate failed: %v", err)
	}
	for _, want := range sliceRoutes {
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
