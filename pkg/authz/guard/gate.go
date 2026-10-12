package guard

import (
	"fmt"
	"sync"

	"github.com/vislake/speed/pkg/authz"
)

// routeKey identifies one route: the HTTP method and the path pattern the entry
// point matched, which the middleware reads off the context as c.FullPath(). The
// key is the pair and never the path alone: the same pattern reached by another
// method is another route, with a declaration of its own.
type routeKey struct{ method, path string }

// gate is the route declaration surface authz.Gate describes: it records the
// permission one (method, path) needs, or records it as open, and is the table
// the enforcement middleware and the decider's sibling faces read.
//
// It holds the catalog it was built over, and verify checks every declared
// permission against it once, after the Init window closes. The gate declares
// nothing itself: a module writes its routes from its own Init callback, so all
// writes fall in the Init window, and once that window closes the table is never
// rewritten. From then on it is read on the request path — requirement and
// isOpen — and the mutex keeps that read safe against the writes of Init and of
// any other writer that reaches it before the window closes. A route redeclared
// is one kind or the other, never both; Require and Open each clear the other.
type gate struct {
	mu sync.Mutex
	// reqs holds the permission each (method, path) needs.
	reqs map[routeKey]authz.Permission
	// open holds the (method, path) declared to need no permission.
	open map[routeKey]bool
	// catalog is the set of declared permissions a requirement is held to.
	catalog authz.Catalog
}

var _ authz.Gate = (*gate)(nil)

// newGate returns a gate over catalog, with no declaration recorded.
func newGate(catalog authz.Catalog) *gate {
	return &gate{
		reqs:    map[routeKey]authz.Permission{},
		open:    map[routeKey]bool{},
		catalog: catalog,
	}
}

// Require implements authz.Gate. It records that (method, path) needs perm, and
// drops an open registration of the same route: a declaration is one or the
// other. The declared permission is checked against the catalog by verify, not
// here — this method reports nothing, and the check needs the Init window to
// have closed so that it holds every declaration at once.
func (g *gate) Require(method, path string, perm authz.Permission) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := routeKey{method, path}
	g.reqs[k] = perm
	delete(g.open, k)
}

// Open implements authz.Gate. It records that (method, path) needs no
// permission, and drops a requirement of the same route: a declaration is one or
// the other.
func (g *gate) Open(method, path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := routeKey{method, path}
	g.open[k] = true
	delete(g.reqs, k)
}

// Covered implements authz.Gate. It reports whether (method, path) carries any
// declaration at all, open or requiring a permission. A route that carries none
// is an undeclared route, which the middleware answers 403 for under the closed
// default.
func (g *gate) Covered(method, path string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := routeKey{method, path}
	if _, ok := g.reqs[k]; ok {
		return true
	}
	return g.open[k]
}

// requirement returns the permission (method, path) needs, and whether it was
// declared to need one. It is the middleware's read on the request path, taken
// with the method and c.FullPath(); a false second result means the route needs
// no permission declared here, which is either an open declaration or none at
// all, and isOpen tells the two apart.
func (g *gate) requirement(method, path string) (authz.Permission, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.reqs[routeKey{method, path}]
	return p, ok
}

// isOpen reports whether (method, path) was declared open. It is the
// middleware's read on the request path, and it asks it only where requirement
// found no requirement.
func (g *gate) isOpen(method, path string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open[routeKey{method, path}]
}

// verify holds every declared permission to the catalog and to the translation,
// and reports the first one it does not hold, wrapped around
// authz.ErrUnknownPermission and naming the route and the permission. It is run
// once after the Init window closes, because Require reports nothing and the
// check has to see every declaration at once: the middleware reads the table
// after the window, so a permission the catalog does not hold is a startup
// failure rather than a route that decides nothing. A permission the catalog
// holds but the translation has no row for is refused the same way: the request
// path decides through the translation, so such a route would deny every request
// forever with no startup symptom.
//
// The failure names the permission. It is a startup diagnostic and not a
// response, so naming it does not open the policy-probing path the 403's detail
// is kept clear of.
func (g *gate) verify() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, perm := range g.reqs {
		if !g.catalog.Has(perm) {
			return fmt.Errorf("%w: %s %s wants %q", authz.ErrUnknownPermission, k.method, k.path, perm)
		}
		if _, _, ok := ObjectAction(perm); !ok {
			return fmt.Errorf("%w: %s %s wants %q, which the translation cannot express",
				authz.ErrUnknownPermission, k.method, k.path, perm)
		}
	}
	return nil
}
