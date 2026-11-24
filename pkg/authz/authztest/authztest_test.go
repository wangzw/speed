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

func probeModule() core.Module {
	return core.Module{
		Name: "probe",
		Requires: []core.Requirement{
			{Token: (*authz.Enforcer)(nil)},
			{Token: (*authz.Catalog)(nil)},
		},
	}
}

// TestRegistryResolvesTheStubCapabilities checks that registry resolves the stub capabilities.
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

// TestRegistryFailsWithoutAStandIn checks that registry fails without a stand in.
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

// TestEnforcerDeniesUntilAnAnswerIsSet checks that enforcer denies until an answer is set.
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

// TestEnforcerAnswersFromTheRecordedFunction checks that enforcer answers from the recorded function.
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

// TestEnforcerFailingIsAnErrorAndNotADenial checks that enforcer failing is an error and not a denial.
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

// TestCatalogDeclaresWhatItWasGivenAndNothingElse checks that catalog declares what it was given and nothing else.
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

// TestCatalogAnswersTheMetadataItWasGiven checks that catalog answers the metadata it was given.
func TestCatalogAnswersTheMetadataItWasGiven(t *testing.T) {
	c := authztest.NewCatalog(authz.PermissionTenantRoleManage, authz.PermissionRoleRead)
	c.DeclareMetadata(authz.PermissionTenantRoleManage, authz.PermissionMeta{Group: "authorization", Dangerous: true})
	c.DeclareMetadata("crm.undeclared", authz.PermissionMeta{Group: "ignored"})

	meta, ok := c.Metadata(authz.PermissionTenantRoleManage)
	if !ok || meta != (authz.PermissionMeta{Group: "authorization", Dangerous: true}) {
		t.Errorf("Metadata(TenantRoleManage) = %+v, %v, want the declared metadata", meta, ok)
	}
	if meta, ok := c.Metadata(authz.PermissionRoleRead); !ok || meta != (authz.PermissionMeta{}) {
		t.Errorf("Metadata(RoleRead) = %+v, %v, want the zero metadata and declared", meta, ok)
	}
	if _, ok := c.Metadata("crm.undeclared"); ok {
		t.Error("Metadata answered for a permission the catalog does not declare")
	}
}

// TestGateRecordsDeclarations checks that gate records declarations.
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

// TestGateListsRequirementsInOrder checks that gate lists requirements in order.
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
