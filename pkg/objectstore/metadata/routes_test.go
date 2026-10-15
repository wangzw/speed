package metadata

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/objectstore"
)

// wantRoutes is the closed route set with the permission each route declares.
var wantRoutes = map[mountedRoute]authz.Permission{
	{http.MethodPost, "/api/v1/objects"}:              PermissionObjectWrite,
	{http.MethodPut, "/api/v1/objects/:id/content"}:   PermissionObjectWrite,
	{http.MethodPost, "/api/v1/objects/:id/complete"}: PermissionObjectWrite,
	{http.MethodGet, "/api/v1/objects"}:               PermissionObjectRead,
	{http.MethodGet, "/api/v1/objects/:id"}:           PermissionObjectRead,
	{http.MethodGet, "/api/v1/objects/:id/content"}:   PermissionObjectRead,
	{http.MethodDelete, "/api/v1/objects/:id"}:        PermissionObjectWrite,
}

// TestTheModuleMountsItsWholeRouteSetWithTheRightPermissions holds that every
// route is mounted on the configured endpoint and declared with the permission
// the design fixes.
func TestTheModuleMountsItsWholeRouteSetWithTheRightPermissions(t *testing.T) {
	router := newStubRouter("public")
	gate := authztest.NewGate()
	h := engines[0].on(t, harness{withRouter: true, router: router, withGate: true, gate: gate})
	h.drive(t, func(_ context.Context, _ *core.Registry) error {
		got := router.endpoint("public").registered()
		if len(got) != len(wantRoutes) {
			t.Fatalf("mounted %d routes (%v), want %d", len(got), got, len(wantRoutes))
		}
		for _, r := range got {
			want, ok := wantRoutes[r]
			if !ok {
				t.Errorf("unexpected route %s %s", r.method, r.path)
				continue
			}
			perm, declared := gate.Requirement(r.method, r.path)
			if !declared {
				t.Errorf("the route %s %s carries no declaration", r.method, r.path)
				continue
			}
			if perm != want {
				t.Errorf("the route %s %s needs %q, want %q", r.method, r.path, perm, want)
			}
		}
		return nil
	})
}

// TestACommandWithoutAnEntryPointStillDeliversObjects holds that the entry point
// is optional: a run that assembles none still delivers the metadata seam and
// mounts nothing.
func TestACommandWithoutAnEntryPointStillDeliversObjects(t *testing.T) {
	h := engines[0].on(t, harness{})
	h.drive(t, func(_ context.Context, reg *core.Registry) error {
		if objects, err := core.Resolve[objectstore.Objects](reg); err != nil || objects == nil {
			t.Fatalf("resolving the object store with no entry point: %v", err)
		}
		return nil
	})
}

// TestAnEntryPointWithNoEndpointListFailsAtInit holds that the endpoint list is
// required only where it is read: a run that assembles an entry point and names
// no endpoint fails wiring, naming the input item, rather than mounting nothing
// silently. The stages before Init are satisfied, so the refusal is Init's.
func TestAnEntryPointWithNoEndpointListFailsAtInit(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		empty := map[string]any{"endpoints": []string{}}

		before := e.on(t, harness{metadata: empty, withRouter: true})
		if err := before.driveTo(t, core.StageNew, nil); err != nil {
			t.Fatalf("the assembly failed before Init: %v", err)
		}

		h := e.on(t, harness{metadata: empty, withRouter: true})
		err := h.driveTo(t, core.StageInit, nil)
		if !errors.Is(err, objectstore.ErrInvalidConfig) {
			t.Fatalf("driving to Init with an entry point and no endpoint list ended with %v, want ErrInvalidConfig", err)
		}
		if !strings.Contains(err.Error(), endpointsKey) {
			t.Errorf("the failure does not name %s: %v", endpointsKey, err)
		}
	})
}

// TestAnEntryPointWithAnUnmountableEndpointListIsRefused holds the two list
// shapes refused beside an empty one: the name that stands for every endpoint,
// which would put the routes on the public ones, and a name written twice.
func TestAnEntryPointWithAnUnmountableEndpointListIsRefused(t *testing.T) {
	for name, endpoints := range map[string][]string{
		"every endpoint": {"*"},
		"a repeat":       {"public", "public"},
	} {
		t.Run(name, func(t *testing.T) {
			forEachEngine(t, func(t *testing.T, e engine) {
				h := e.on(t, harness{metadata: map[string]any{"endpoints": endpoints}, withRouter: true})
				err := h.driveTo(t, core.StageInit, nil)
				if !errors.Is(err, objectstore.ErrInvalidConfig) {
					t.Fatalf("driving to Init with the endpoint list %v ended with %v, want ErrInvalidConfig",
						endpoints, err)
				}
				if !strings.Contains(err.Error(), endpointsKey) {
					t.Errorf("the failure does not name %s: %v", endpointsKey, err)
				}
			})
		})
	}
}

// TestNoEntryPointReadsNoEndpointList holds that a run with no entry point
// mounts nothing and so reads the endpoint list nowhere: an empty list is a
// legal configuration there, and so is one that would be refused beside an entry
// point. The module comes up delivering the metadata seam either way.
func TestNoEntryPointReadsNoEndpointList(t *testing.T) {
	for name, endpoints := range map[string][]string{
		"empty":          {},
		"every endpoint": {"*"},
	} {
		t.Run(name, func(t *testing.T) {
			forEachEngine(t, func(t *testing.T, e engine) {
				h := e.on(t, harness{metadata: map[string]any{"endpoints": endpoints}})
				h.drive(t, func(_ context.Context, reg *core.Registry) error {
					if objects, err := core.Resolve[objectstore.Objects](reg); err != nil || objects == nil {
						t.Fatalf("resolving the object store with no entry point and the endpoint list %v: %v",
							endpoints, err)
					}
					return nil
				})
			})
		})
	}
}
