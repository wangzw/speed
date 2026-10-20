// Package authztest builds the fixtures a test of the authorization capability
// drives: the configuration reader its modules read, a registry wired the way a
// host wires one, and stand-ins for the capabilities a module requires.
//
// It lives beside the root package and not under internal/, because the policy
// and enforcement subpackages import it from their own tests: a package under
// pkg/authz grants every other package of that tree access to it, and it is
// those subpackages' tests that need it. What keeps it out of production code is
// the dependency test in the parent directory, which fails when any package's
// non-test code imports it, the way the authentication fixture is held.
//
// It imports the root package pkg/authz and never the policy or enforcement
// subpackage: those two import authztest from their tests, so an import back
// would be a cycle, and the stubs below implement the root interfaces alone.
// A self-check in this package holds that limit.
//
// Every fixture takes a testing.TB, but that is no barrier to a host: a
// *testing.T that nobody runs is a valid one. The stubs are the point of the
// package, not the handle.
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

// The stubs implement the root interfaces the policy and enforcement modules
// deliver and take up, so that a stub and the interface cannot drift apart
// without failing the build.
var (
	_ authz.Enforcer = (*Enforcer)(nil)
	_ authz.Catalog  = (*Catalog)(nil)
	_ authz.Gate     = (*Gate)(nil)
)

// The names the stand-in modules register under. A name is the module's identity
// in the registry, so it does not collide with a real module's: the policy and
// enforcement modules carry authz.policy and authz.guard, and a stand-in is
// neither of them.
const (
	// EnforcerModule, CatalogModule and GateModule are the names of the
	// stand-in modules the provider constructors below register.
	EnforcerModule = "authztest.enforcer"
	CatalogModule  = "authztest.catalog"
	GateModule     = "authztest.gate"
)

// Enforcer is a stand-in authz.Enforcer. It answers from what a case recorded
// and records every query it was asked, so a case can hold both what the
// enforcement point put to the engine and what it did with the answer.
//
// The zero value denies every query and fails nothing, which is the fail-closed
// default the real engine has too: an engine with no matching policy row
// refuses, and a stand-in that allowed by default would let a case pass on a
// decision it never recorded.
type Enforcer struct {
	mu    sync.Mutex
	allow func(authz.Query) bool
	err   error
	asked []authz.Query
}

// NewEnforcer returns an Enforcer that denies every query.
func NewEnforcer() *Enforcer { return &Enforcer{} }

// Answer sets the function that decides each query, replacing any earlier one. A
// nil function denies, so an Enforcer on which Answer was never called and one
// given nil answer the same way.
func (e *Enforcer) Answer(fn func(authz.Query) bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.allow = fn
}

// Failing makes every later Allowed return err, standing for an engine or store
// that is unavailable. The query is still recorded. A failing Enforcer answers
// with no permission and no denial: the caller is to read it as a 503, so a
// stand-in that turned the error into a denial would hide the direction the
// design fixes.
func (e *Enforcer) Failing(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.err = err
}

// Asked returns the queries Allowed was called with, in call order. The slice is
// a copy, so a caller may keep it across later calls.
func (e *Enforcer) Asked() []authz.Query {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.asked)
}

// Allowed implements authz.Enforcer. It records the query, then answers: the
// recorded error if Failing set one, else the recorded function's verdict, else
// a denial. It is safe to call from many goroutines.
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

// Catalog is a stand-in authz.Catalog over a fixed set of permissions. Its set
// is settled at construction and never changes, so it needs no lock.
type Catalog struct {
	perms []authz.Permission
	set   map[authz.Permission]bool
}

// NewCatalog returns a Catalog declaring exactly perms.
func NewCatalog(perms ...authz.Permission) *Catalog {
	set := make(map[authz.Permission]bool, len(perms))
	for _, p := range perms {
		set[p] = true
	}
	return &Catalog{perms: slices.Clone(perms), set: set}
}

// Declared implements authz.Catalog. The slice is a copy, as the interface
// requires: a caller sorting or trimming it does not disturb what the next
// caller sees.
func (c *Catalog) Declared() []authz.Permission { return slices.Clone(c.perms) }

// Has implements authz.Catalog.
func (c *Catalog) Has(p authz.Permission) bool { return c.set[p] }

// ObjectAction implements authz.Catalog by reading the process's translation.
//
// It is not a stand-in of its own: a permission's meaning is one thing for the
// whole process, so a case that fastened a pair of its own here would be
// asserting against a translation no decision is made by. A case that wants a
// pair for a permission it declares resolves one through the translation package
// the way an assembly does.
func (c *Catalog) ObjectAction(p authz.Permission) (object, action string, ok bool) {
	return translation.Lookup(p)
}

// DescriptionKey implements authz.Catalog. The stand-in carries no description
// keys of its own: a case that wants one declares the PermissionSpec that holds
// it and builds the real catalog, the way an assembly does.
func (c *Catalog) DescriptionKey(p authz.Permission) (key string, ok bool) {
	return "", c.set[p]
}

// routeKey is one (method, path) declaration's key.
type routeKey struct{ method, path string }

// Gate is a stand-in authz.Gate. It records what each (method, path) was
// declared to need, or that it was declared open, so a case can read back what a
// module registered at Init without running the enforcement middleware. It
// carries no catalog check: the real gate refuses a declaration naming a
// permission the catalog does not hold, and a case that wants that failure
// drives the real gate. It carries no translation check either, and that is
// deliberate: the real gate refuses a declaration naming a permission the
// process's translation — the built-in rows unioned with every module's declared
// rows — has no row for, and this stand-in holds no such set. A case that wants
// that failure drives the real gate over a resolved translation.
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
// requiring, and the open registration is dropped: a declaration is one or the
// other.
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
// then path order. Routes declared open are not in it; a case that holds both
// sets reads IsOpen for the other. The slice is a copy.
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
// and the name the stand-in registers under. AddProvider puts it in a registry,
// so that a module requiring the capability resolves the instance and a case
// drives that module without importing the capability's real deliverer.
type Provider struct {
	// Name is the stand-in module's name.
	Name string
	// Token is the capability it delivers, written as a typed nil pointer.
	Token core.Token
	// Instance delivers Token.
	Instance any
}

// AddProvider registers the stand-in module and supplies its instance for it.
// The module makes no exclusive claim: the stand-in stands where the capability's
// real deliverer would, and a claim of its own would stand beside the real one
// rather than in place of it.
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
// configuration module with a reader built from body and supplied for it, and
// the given modules registered.
//
// Every module whose configuration the reader has to serve is passed here, so
// that the reader knows its declarations; the modules are registered as well, so
// that a case drives them without a second registration step. A case with no
// configuration passes none and an empty body.
//
// The skeleton wires the loader the way a host does — the reader supplied to
// config.ModuleName — and nothing else. A capability the given modules require
// is not added here: the case adds each with AddProvider before driving, so that
// a required capability with no stand-in fails the drive as it would in a host
// that left the deliverer out of the command.
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
