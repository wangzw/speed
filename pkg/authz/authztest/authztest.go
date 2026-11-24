// Package authztest builds the fixtures a test of the authorization capability
// drives: the configuration reader its modules read, a registry wired the way a
// host wires one, and stand-ins for the capabilities a module requires.
//
// It imports the root package pkg/authz and never the policy or enforcement
// subpackage; a dependency test holds it out of production code.
package authztest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/internal/translation"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
)

var (
	_ authz.Enforcer = (*Enforcer)(nil)
	_ authz.Catalog  = (*Catalog)(nil)
	_ authz.Gate     = (*Gate)(nil)
)

// The names the stand-in modules register under.
const (
	// EnforcerModule, CatalogModule and GateModule are the names of the
	// stand-in modules the provider constructors below register.
	EnforcerModule = "authztest.enforcer"
	CatalogModule  = "authztest.catalog"
	GateModule     = "authztest.gate"
)

// Enforcer is a stand-in authz.Enforcer. It answers from what a case recorded
// and records every query it was asked.
//
// The zero value denies every query and fails nothing.
type Enforcer struct {
	mu    sync.Mutex
	allow func(authz.Query) bool
	err   error
	asked []authz.Query
}

// NewEnforcer returns an Enforcer that denies every query.
func NewEnforcer() *Enforcer { return &Enforcer{} }

// Answer sets the function that decides each query, replacing any earlier one.
// A nil function denies.
func (e *Enforcer) Answer(fn func(authz.Query) bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.allow = fn
}

// Failing makes every later Allowed return err, standing for an engine or store
// that is unavailable. The query is still recorded.
func (e *Enforcer) Failing(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.err = err
}

// Asked returns the queries Allowed was called with, in call order, as a copy.
func (e *Enforcer) Asked() []authz.Query {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.asked)
}

// Allowed implements authz.Enforcer. It records the query, then answers the
// recorded error, else the recorded function's verdict, else a denial.
func (e *Enforcer) Allowed(_ context.Context, q authz.Query) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.asked = append(e.asked, q)
	if e.err != nil {
		return false, e.err
	}
	if e.allow != nil {
		return e.allow(q), nil
	}
	return false, nil
}

// Catalog is a stand-in authz.Catalog over a fixed set of permissions.
type Catalog struct {
	perms       []authz.Permission
	set         map[authz.Permission]bool
	governed    []authz.Permission
	governedSet map[authz.Permission]bool
	metadata    map[authz.Permission]authz.PermissionMeta
}

// NewCatalog returns a Catalog declaring exactly perms.
func NewCatalog(perms ...authz.Permission) *Catalog {
	set := make(map[authz.Permission]bool, len(perms))
	for _, p := range perms {
		set[p] = true
	}
	return &Catalog{
		perms:       slices.Clone(perms),
		set:         set,
		governedSet: map[authz.Permission]bool{},
		metadata:    map[authz.Permission]authz.PermissionMeta{},
	}
}

// DeclareGoverned puts the permissions among perms that were declared into the
// governed set, in permission-name order. A permission not declared is ignored.
func (c *Catalog) DeclareGoverned(perms ...authz.Permission) {
	c.governed = nil
	c.governedSet = make(map[authz.Permission]bool, len(perms))
	for _, p := range perms {
		if !c.set[p] || c.governedSet[p] {
			continue
		}
		c.governedSet[p] = true
		c.governed = append(c.governed, p)
	}
	slices.Sort(c.governed)
}

// Declared implements authz.Catalog. The slice is a copy.
func (c *Catalog) Declared() []authz.Permission { return slices.Clone(c.perms) }

// TenantGoverned implements authz.Catalog. The slice is a copy.
func (c *Catalog) TenantGoverned() []authz.Permission { return slices.Clone(c.governed) }

// Governed implements authz.Catalog.
func (c *Catalog) Governed(p authz.Permission) bool { return c.governedSet[p] && c.set[p] }

// Has implements authz.Catalog.
func (c *Catalog) Has(p authz.Permission) bool { return c.set[p] }

// ObjectAction implements authz.Catalog by reading the process's translation.
func (c *Catalog) ObjectAction(p authz.Permission) (object, action string, ok bool) {
	return translation.Lookup(p)
}

// PermissionOf implements authz.Catalog by reversing the process's translation
// over the permissions the stand-in declares.
func (c *Catalog) PermissionOf(object, action string) (authz.Permission, bool) {
	for _, p := range c.perms {
		o, a, ok := translation.Lookup(p)
		if ok && o == object && a == action {
			return p, true
		}
	}
	return "", false
}

// DescriptionKey implements authz.Catalog. The stand-in carries no description
// keys of its own.
func (c *Catalog) DescriptionKey(p authz.Permission) (key string, ok bool) {
	return "", c.set[p]
}

// DeclareMetadata gives one declared permission the governance metadata a
// declaring module would have put in its PermissionSpec.
func (c *Catalog) DeclareMetadata(p authz.Permission, meta authz.PermissionMeta) {
	if !c.set[p] {
		return
	}
	c.metadata[p] = meta
}

// Metadata implements authz.Catalog. A declared permission a case did not
// describe answers the zero metadata and true.
func (c *Catalog) Metadata(p authz.Permission) (authz.PermissionMeta, bool) {
	if !c.set[p] {
		return authz.PermissionMeta{}, false
	}
	return c.metadata[p], true
}

type routeKey struct{ method, path string }

// Gate is a stand-in authz.Gate. It records what each (method, path) was
// declared to need, or that it was declared open. It carries no catalog or
// translation check.
type Gate struct {
	mu   sync.Mutex
	reqs map[routeKey]authz.Permission
	open map[routeKey]bool
}

// NewGate returns a Gate that has recorded nothing.
func NewGate() *Gate {
	return &Gate{reqs: map[routeKey]authz.Permission{}, open: map[routeKey]bool{}}
}

// Require implements authz.Gate. A route already declared open is recorded as
// requiring, and the open registration is dropped.
func (g *Gate) Require(method, path string, perm authz.Permission) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := routeKey{method, path}
	g.reqs[k] = perm
	delete(g.open, k)
}

// Open implements authz.Gate. A route already declared to require a permission
// is recorded as open, and the requirement is dropped.
func (g *Gate) Open(method, path string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := routeKey{method, path}
	g.open[k] = true
	delete(g.reqs, k)
}

// Covered implements authz.Gate.
func (g *Gate) Covered(method, path string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	k := routeKey{method, path}
	if _, ok := g.reqs[k]; ok {
		return true
	}
	return g.open[k]
}

// Requirement returns the permission (method, path) was declared to need, and
// whether it was declared that way at all.
func (g *Gate) Requirement(method, path string) (authz.Permission, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.reqs[routeKey{method, path}]
	return p, ok
}

// IsOpen reports whether (method, path) was declared open.
func (g *Gate) IsOpen(method, path string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open[routeKey{method, path}]
}

// Requirements returns every route declared to require a permission, in method
// then path order, as a copy.
func (g *Gate) Requirements() []authz.Requirement {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]authz.Requirement, 0, len(g.reqs))
	for k, p := range g.reqs {
		out = append(out, authz.Requirement{Method: k.method, Path: k.path, Permission: p})
	}
	slices.SortFunc(out, func(a, b authz.Requirement) int {
		if c := strings.Compare(a.Method, b.Method); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

// Provider is a stand-in module: a capability, the instance that delivers it,
// and the name the stand-in registers under.
type Provider struct {
	// Name is the stand-in module's name.
	Name string
	// Token is the capability it delivers, written as a typed nil pointer.
	Token core.Token
	// Instance delivers Token.
	Instance any
}

// AddProvider registers the stand-in module and supplies its instance for it.
func AddProvider(reg *core.Registry, p Provider) {
	reg.Register(core.Module{Name: p.Name, Provides: []core.Provision{{Token: p.Token}}})
	reg.Supply(p.Instance, p.Name)
}

// EnforcerProvider is a Provider delivering e as an authz.Enforcer, under
// EnforcerModule.
func EnforcerProvider(e authz.Enforcer) Provider {
	return Provider{Name: EnforcerModule, Token: (*authz.Enforcer)(nil), Instance: e}
}

// CatalogProvider is a Provider delivering c as an authz.Catalog, under
// CatalogModule.
func CatalogProvider(c authz.Catalog) Provider {
	return Provider{Name: CatalogModule, Token: (*authz.Catalog)(nil), Instance: c}
}

// GateProvider is a Provider delivering g as an authz.Gate, under GateModule.
func GateProvider(g authz.Gate) Provider {
	return Provider{Name: GateModule, Token: (*authz.Gate)(nil), Instance: g}
}

// Registry builds the assembly skeleton: a fresh registry holding the
// configuration module with a reader built from body, and the given modules
// registered. A capability the given modules require is not added here.
func Registry(t testing.TB, body string, modules ...core.Module) *core.Registry {
	t.Helper()
	reader := ConfigReaderAll(t, body, modules...)

	reg := core.New()
	reg.Register(config.Module())
	reg.Supply(reader, config.ModuleName)
	for _, m := range modules {
		reg.Register(m)
	}
	return reg
}
