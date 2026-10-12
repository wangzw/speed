package authztest_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/authz/authztest"
	"github.com/vislake/speed/pkg/core"
)

const (
	alice = "acct-alice"
	acme  = "acme"
)

// probeModule requires the two capabilities a guarded assembly supplies, so a
// drive that resolves them holds the skeleton to wiring a real registry and not
// to a list of stand-ins the case passed around by hand.
func probeModule() core.Module {
	return core.Module{
		Name: "probe",
		Requires: []core.Requirement{
			{Token: (*authz.Enforcer)(nil)},
			{Token: (*authz.Catalog)(nil)},
		},
	}
}

// TestRegistryResolvesTheStubCapabilities drives an assembly to Init and asks it
// for the two capabilities the probe requires. What it holds is the whole of the
// skeleton: the registry resolves the supplied stubs by the tokens the probe
// declared, the stubs are the very ones supplied, and the engine can be driven
// once from inside the drive.
func TestRegistryResolvesTheStubCapabilities(t *testing.T) {
	enforcer := authztest.NewEnforcer()
	enforcer.Answer(func(authz.Query) bool { return true })
	catalog := authztest.NewCatalog(authz.PermissionRoleRead, authz.PermissionTenantAccess)

	reg := authztest.Registry(t, `{}`, probeModule())
	authztest.AddProvider(reg, authztest.EnforcerProvider(enforcer))
	authztest.AddProvider(reg, authztest.CatalogProvider(catalog))

	query := authz.Query{Subject: alice, Domain: acme, Object: "role", Action: "read"}
	var gotEnforcer authz.Enforcer
	var gotCatalog authz.Catalog

	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageInit,
		Participants: []string{"probe"},
		Action: func(ctx context.Context, reg *core.Registry) error {
			e, err := reg.Resolve((*authz.Enforcer)(nil))
			if err != nil {
				return err
			}
			gotEnforcer = e.(authz.Enforcer)
			c, err := reg.Resolve((*authz.Catalog)(nil))
			if err != nil {
				return err
			}
			gotCatalog = c.(authz.Catalog)
			_, err = gotEnforcer.Allowed(ctx, query)
			return err
		},
	})
	if err != nil {
		t.Fatalf("driving the assembly: %v", err)
	}
	if gotEnforcer != authz.Enforcer(enforcer) {
		t.Errorf("Resolve gave %T, want the supplied enforcer", gotEnforcer)
	}
	if gotCatalog != authz.Catalog(catalog) {
		t.Errorf("Resolve gave %T, want the supplied catalog", gotCatalog)
	}
	if got := enforcer.Asked(); !slices.Equal(got, []authz.Query{query}) {
		t.Errorf("the engine was asked %v, want %v", got, []authz.Query{query})
	}
}

// TestRegistryFailsWithoutAStandIn holds that the skeleton does not supply a
// capability on its own: a required capability with no stand-in fails the drive,
// which is what a host that left the deliverer out of its command gets. A
// skeleton that quietly provided one would make every case pass whatever the
// assembly held.
func TestRegistryFailsWithoutAStandIn(t *testing.T) {
	reg := authztest.Registry(t, `{}`, probeModule())
	err := reg.Drive(context.Background(), core.Drive{
		Depth:        core.StageNew,
		Participants: []string{"probe"},
	})
	if !errors.Is(err, core.ErrMissingProvider) {
		t.Fatalf("a drive with no stand-in returned %v, want ErrMissingProvider", err)
	}
}

// TestEnforcerDeniesUntilAnAnswerIsSet holds the fail-closed default: a fresh
// Enforcer refuses, and so does one whose Answer was given nil. An engine with
// no matching rule refuses, and a stand-in that allowed by default would let a
// case pass on a decision it never recorded.
func TestEnforcerDeniesUntilAnAnswerIsSet(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*authztest.Enforcer)
	}{
		{"a fresh one", func(*authztest.Enforcer) {}},
		{"one answered with nil", func(e *authztest.Enforcer) { e.Answer(nil) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := authztest.NewEnforcer()
			c.set(e)
			got, err := e.Allowed(context.Background(), authz.Query{Subject: alice, Domain: acme})
			if err != nil {
				t.Fatalf("Allowed returned an error: %v", err)
			}
			if got {
				t.Error("Allowed returned true, want the fail-closed denial")
			}
		})
	}
}

// TestEnforcerAnswersFromTheRecordedFunction holds that the recorded function
// decides each query, so a case can allow some subjects and deny others.
func TestEnforcerAnswersFromTheRecordedFunction(t *testing.T) {
	e := authztest.NewEnforcer()
	e.Answer(func(q authz.Query) bool { return q.Subject == alice })

	for _, c := range []struct {
		subject string
		want    bool
	}{{alice, true}, {"acct-bob", false}} {
		got, err := e.Allowed(context.Background(), authz.Query{Subject: c.subject})
		if err != nil {
			t.Fatalf("Allowed(%s): %v", c.subject, err)
		}
		if got != c.want {
			t.Errorf("Allowed(%s) = %v, want %v", c.subject, got, c.want)
		}
	}
}

// TestEnforcerFailingIsAnErrorAndNotADenial holds the direction the design
// fixes: an unavailable engine answers with the error and no permission, never a
// denial, so the caller reads it as a 503.
func TestEnforcerFailingIsAnErrorAndNotADenial(t *testing.T) {
	boom := errors.New("engine down")
	e := authztest.NewEnforcer()
	e.Failing(boom)

	got, err := e.Allowed(context.Background(), authz.Query{Subject: alice, Domain: acme})
	if !errors.Is(err, boom) {
		t.Fatalf("Allowed returned %v, want the recorded error", err)
	}
	if got {
		t.Error("a failing engine allowed a query")
	}
	if asks := e.Asked(); len(asks) != 1 {
		t.Errorf("a failing engine recorded %d queries, want the one it was asked", len(asks))
	}
}

// TestCatalogDeclaresWhatItWasGivenAndNothingElse holds both directions of the
// declaration: what was given is held, what was not is not, and the returned
// slice is a copy the caller's edits do not reach through.
func TestCatalogDeclaresWhatItWasGivenAndNothingElse(t *testing.T) {
	c := authztest.NewCatalog(authz.PermissionRoleRead, authz.PermissionTenantAccess)

	want := []authz.Permission{authz.PermissionRoleRead, authz.PermissionTenantAccess}
	if got := c.Declared(); !slices.Equal(got, want) {
		t.Errorf("Declared() = %v, want %v", got, want)
	}
	if !c.Has(authz.PermissionRoleRead) {
		t.Error("Has(RoleRead) = false for a declared permission")
	}
	if c.Has(authz.PermissionRoleManage) {
		t.Error("Has(RoleManage) = true for a permission never declared")
	}
	if got := authztest.NewCatalog().Declared(); len(got) != 0 {
		t.Errorf("an empty Catalog declares %v", got)
	}

	declared := c.Declared()
	declared[0] = "mutated"
	if c.Declared()[0] != authz.PermissionRoleRead {
		t.Error("Declared() handed back a slice the caller's edits leak through")
	}
}

// TestGateRecordsDeclarations holds that a declaration is read back as the kind
// it was made: a required permission, or open, and never both. A route
// redeclared flips its kind, as the interface's one required-or-open shape says.
func TestGateRecordsDeclarations(t *testing.T) {
	g := authztest.NewGate()
	g.Require("GET", "/accounts", authz.PermissionAccountRead)
	g.Open("GET", "/health")

	if p, ok := g.Requirement("GET", "/accounts"); !ok || p != authz.PermissionAccountRead {
		t.Errorf("Requirement(GET /accounts) = %q, %v; want the declared permission", p, ok)
	}
	if _, ok := g.Requirement("GET", "/health"); ok {
		t.Error("a route declared open carries a requirement")
	}
	if !g.IsOpen("GET", "/health") {
		t.Error("IsOpen is false for a route declared open")
	}
	if g.IsOpen("GET", "/accounts") {
		t.Error("IsOpen is true for a route that requires a permission")
	}
	if !g.Covered("GET", "/accounts") || !g.Covered("GET", "/health") {
		t.Error("Covered is false for a declared route")
	}
	if g.Covered("GET", "/undeclared") {
		t.Error("Covered is true for a route never declared")
	}

	g.Open("GET", "/accounts")
	if _, ok := g.Requirement("GET", "/accounts"); ok {
		t.Error("a route redeclared open still carries its requirement")
	}
	if !g.IsOpen("GET", "/accounts") {
		t.Error("a route redeclared open is not open")
	}

	g.Require("GET", "/health", authz.PermissionAccountRead)
	if g.IsOpen("GET", "/health") {
		t.Error("a route redeclared as requiring is still open")
	}
	if p, ok := g.Requirement("GET", "/health"); !ok || p != authz.PermissionAccountRead {
		t.Errorf("Requirement(GET /health) = %q, %v after redeclaring", p, ok)
	}
}

// TestGateListsRequirementsInOrder holds the whole-declaration read: only the
// routes that require a permission, in method then path order, so a case can
// assert what a module registered at Init against the set it meant.
func TestGateListsRequirementsInOrder(t *testing.T) {
	g := authztest.NewGate()
	g.Require("POST", "/accounts", authz.PermissionAccountCreate)
	g.Require("GET", "/accounts", authz.PermissionAccountRead)
	g.Require("GET", "/accounts/{id}", authz.PermissionAccountRead)
	g.Open("GET", "/health")

	want := []authz.Requirement{
		{Method: "GET", Path: "/accounts", Permission: authz.PermissionAccountRead},
		{Method: "GET", Path: "/accounts/{id}", Permission: authz.PermissionAccountRead},
		{Method: "POST", Path: "/accounts", Permission: authz.PermissionAccountCreate},
	}
	if got := g.Requirements(); !slices.Equal(got, want) {
		t.Errorf("Requirements() = %v, want %v", got, want)
	}
}
