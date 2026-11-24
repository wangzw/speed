package guard

import (
	"fmt"
	"sync"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
)

type routeKey struct{ method, path string }

type gate struct {
	mu      sync.Mutex
	reqs    map[routeKey]authz.Permission
	open    map[routeKey]bool
	catalog authz.Catalog
}

var _ authz.Gate = (*gate)(nil)

func newGate(catalog authz.Catalog) *gate {
	return &gate{
		reqs:    map[routeKey]authz.Permission{},
		open:    map[routeKey]bool{},
		catalog: catalog,
	}
}

func (g *gate) Require(method, path string, perm authz.Permission) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := routeKey{method, path}
	g.reqs[k] = perm
	delete(g.open, k)
}

func (g *gate) Open(method, path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := routeKey{method, path}
	g.open[k] = true
	delete(g.reqs, k)
}

func (g *gate) Covered(method, path string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := routeKey{method, path}
	if _, ok := g.reqs[k]; ok {
		return true
	}
	return g.open[k]
}

func (g *gate) requirement(method, path string) (authz.Permission, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.reqs[routeKey{method, path}]
	return p, ok
}

func (g *gate) isOpen(method, path string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open[routeKey{method, path}]
}

func (g *gate) verify() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, perm := range g.reqs {
		if !g.catalog.Has(perm) {
			return fmt.Errorf("%w: %s %s wants %q", authz.ErrUnknownPermission, k.method, k.path, perm)
		}
		if _, _, ok := translation.Lookup(perm); !ok {
			return fmt.Errorf("%w: %s %s wants %q, which the translation cannot express",
				authz.ErrUnknownPermission, k.method, k.path, perm)
		}
	}
	return nil
}
