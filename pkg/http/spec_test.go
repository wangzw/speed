package http

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/core"
)

const validFragment = `{"openapi":"3.1.0","paths":{"/things":{}}}`

// TestSpecRejectsEmptyDocument checks that an empty document fails the startup.
func TestSpecRejectsEmptyDocument(t *testing.T) {
	for _, document := range []string{"", "   \n\t"} {
		err := validateSpec("billing", Spec{Endpoint: "public", Document: []byte(document)})
		assertInvalidSpec(t, err, "billing", "empty")
	}
	err := validateSpec("billing", Spec{Endpoint: "public"})
	assertInvalidSpec(t, err, "billing", "empty")
}

// TestSpecRejectsNonUTF8 checks that a document that is not UTF-8 fails the startup.
func TestSpecRejectsNonUTF8(t *testing.T) {
	document := []byte(`{"openapi":"3.1.0","info":{"title":"` + "\xff\xfe" + `"}}`)
	err := validateSpec("billing", Spec{Endpoint: "public", Document: document})
	assertInvalidSpec(t, err, "billing", "UTF-8")
}

// TestSpecRejectsMalformedJSON checks that a document that is not valid JSON fails the startup.
func TestSpecRejectsMalformedJSON(t *testing.T) {
	err := validateSpec("billing", Spec{Endpoint: "public", Document: []byte(`{"openapi":`)})
	assertInvalidSpec(t, err, "billing", "JSON")
}

// TestSpecRejectsNonObjectTopLevel checks that a document whose top level is not an object fails
// the startup.
func TestSpecRejectsNonObjectTopLevel(t *testing.T) {
	for _, document := range []string{`["openapi"]`, `"3.1.0"`, `42`, `null`} {
		err := validateSpec("billing", Spec{Endpoint: "public", Document: []byte(document)})
		assertInvalidSpec(t, err, "billing", "object at the top level")
	}
}

// TestSpecRejectsMissingOpenAPIField checks that a document without an openapi field fails the
// startup.
func TestSpecRejectsMissingOpenAPIField(t *testing.T) {
	err := validateSpec("billing", Spec{Endpoint: "public", Document: []byte(`{"paths":{"/things":{}}}`)})
	assertInvalidSpec(t, err, "billing", "openapi")
}

// TestSpecAcceptsAWellShapedFragment is the positive half: the five refusals above would all hold
// in an implementation that refuses everything.
func TestSpecAcceptsAWellShapedFragment(t *testing.T) {
	if err := validateSpec("billing", Spec{Endpoint: "public", Document: []byte(validFragment)}); err != nil {
		t.Fatalf("a well-shaped fragment was refused: %v", err)
	}
}

// TestSpecErrorNamesDeclaringModule checks that a Spec failure names the module that declared it.
func TestSpecErrorNamesDeclaringModule(t *testing.T) {
	err := validateSpecs([]core.Resource[Spec]{
		{Module: "billing", Value: Spec{Endpoint: "public", Document: []byte(`{"paths":{}}`)}},
		{Module: "tenancy", Value: Spec{Endpoint: "public", Document: []byte(`nonsense`)}},
		{Module: "healthy", Value: Spec{Endpoint: "public", Document: []byte(validFragment)}},
	})
	if err == nil {
		t.Fatal("two unusable fragments produced no failure")
	}
	if !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("the failure does not match ErrInvalidSpec: %v", err)
	}
	for _, module := range []string{"billing", "tenancy"} {
		if !strings.Contains(err.Error(), module) {
			t.Errorf("the failure %q does not name %q, whose fragment is unusable", err, module)
		}
	}
	if strings.Contains(err.Error(), "healthy") {
		t.Errorf("the failure %q names healthy, whose fragment is fine", err)
	}
}

// TestAPIRootJoinsBareSegments pins the composition the merge and the route contract both rely on:
// Prefix and Version are bare segments, and the reader supplies the slashes.
func TestAPIRootJoinsBareSegments(t *testing.T) {
	for _, c := range []struct {
		spec Spec
		root string
	}{
		{Spec{}, ""},
		{Spec{Prefix: "api"}, "/api"},
		{Spec{Version: "v1"}, "/v1"},
		{Spec{Prefix: "api", Version: "v1"}, "/api/v1"},
		{Spec{Prefix: "internal/api", Version: "v1", Scope: ScopeSystem}, "/internal/api/v1/system"},
	} {
		if got := c.spec.APIRoot(); got != c.root {
			t.Errorf("APIRoot of Prefix %q and Version %q is %q, want %q",
				c.spec.Prefix, c.spec.Version, got, c.root)
		}
		if got := c.spec.ResolvePath("/things"); got != c.root+"/things" {
			t.Errorf("ResolvePath(/things) under %q is %q, want %q", c.root, got, c.root+"/things")
		}
	}
	if got := (Spec{Prefix: "api", Version: "v1"}).ResolvePath("/"); got != "/api/v1" {
		t.Errorf("ResolvePath(/) under /api/v1 is %q, want the root itself", got)
	}
}

// TestSpecRejectsAPrefixWrittenWithSlashes holds the form Prefix takes: bare segments.
func TestSpecRejectsAPrefixWrittenWithSlashes(t *testing.T) {
	err := validateSpec("billing", Spec{
		Endpoint: "public", Prefix: "/api",
		Document: []byte(validFragment),
	})
	assertInvalidSpec(t, err, "billing", "bare segments")
}

// TestSpecRejectsNonLiteralPrefixOrVersion covers the values that would stop composing a path a
// client can address.
func TestSpecRejectsNonLiteralPrefixOrVersion(t *testing.T) {
	for _, c := range []struct {
		reason string
		spec   Spec
	}{
		{"literal segment", Spec{Prefix: "internal//api"}},
		{"literal segment", Spec{Prefix: "api/../internal"}},
		{"literal segment", Spec{Prefix: "api:v1"}},
		{"literal segment", Spec{Version: "v1/beta"}},
		{"literal segment", Spec{Version: ".."}},
	} {
		c.spec.Endpoint = "public"
		c.spec.Document = []byte(validFragment)
		err := validateSpec("billing", c.spec)
		assertInvalidSpec(t, err, "billing", c.reason)
	}
}

// TestSpecRejectsPathsThatAlreadyCarryTheRoot holds a fragment to the relative form: a path written
// with the Prefix and Version it already declares would reach the client under the root twice.
func TestSpecRejectsPathsThatAlreadyCarryTheRoot(t *testing.T) {
	document := []byte(`{"openapi":"3.1.0","paths":{"/api/v1/auth":{}}}`)
	err := validateSpec("billing", Spec{
		Endpoint: "public", Prefix: "api", Version: "v1",
		Document: document,
	})
	assertInvalidSpec(t, err, "billing", "already carries the API root")
}

// TestSpecAcceptsRelativePathsUnderARoot is the positive half of the two refusals above.
func TestSpecAcceptsRelativePathsUnderARoot(t *testing.T) {
	if err := validateSpec("billing", Spec{
		Endpoint: "public", Prefix: "api", Version: "v1",
		Document: []byte(validFragment),
	}); err != nil {
		t.Fatalf("a fragment with relative paths under a root was refused: %v", err)
	}
}

// TestSpecUnknownEndpointIsNotAFailure pins one of the accepted costs: a Spec naming
// an undeclared endpoint is not a failure.
func TestSpecUnknownEndpointIsNotAFailure(t *testing.T) {
	err := validateSpecs([]core.Resource[Spec]{
		{Module: "billing", Value: Spec{Endpoint: "an-endpoint-nobody-declared", Document: []byte(validFragment)}},
	})
	if err != nil {
		t.Fatalf("a fragment pointing at an undeclared endpoint failed the startup: %v. "+
			"The design accepts that Spec and the registrations drift apart", err)
	}
}

// TestSpecsWithNothingDeclaredIsNotAFailure covers the ordinary host that declares no fragment at
// all.
func TestSpecsWithNothingDeclaredIsNotAFailure(t *testing.T) {
	if err := validateSpecs(nil); err != nil {
		t.Fatalf("an assembly declaring no fragment failed: %v", err)
	}
}

// TestAnOrdinarySpecCarriesNoScope holds the ordinary surface's shape: a fragment under the API
// root with no scope is published directly below the root, and its paths carry no level segment.
func TestAnOrdinarySpecCarriesNoScope(t *testing.T) {
	spec := Spec{Endpoint: "public", Prefix: "api", Version: "v1", Document: []byte(validFragment)}
	if err := validateSpec("billing", spec); err != nil {
		t.Fatalf("an ordinary fragment was refused: %v", err)
	}
	if got := spec.ResolvePath("/auth/me"); got != "/api/v1/auth/me" {
		t.Errorf("an ordinary path resolves to %q", got)
	}
}

// TestSpecRefusesAnUnknownScope holds the closed set: a level nobody decided on is a declaration
// defect, not a value to carry through the merge.
func TestSpecRefusesAnUnknownScope(t *testing.T) {
	err := validateSpec("billing", Spec{
		Endpoint: "public", Prefix: "api", Version: "v1", Scope: Scope("admin"),
		Document: []byte(validFragment),
	})
	assertInvalidSpec(t, err, "billing", "not one of")
}

// TestATenantsScopePathNamesTheTenantFirst holds the shape the tenant level prescribes: the tenant
// is part of the address.
func TestATenantsScopePathNamesTheTenantFirst(t *testing.T) {
	err := validateSpec("billing", Spec{
		Endpoint: "public", Prefix: "api", Version: "v1", Scope: ScopeTenants,
		Document: []byte(`{"openapi":"3.1.0","paths":{"/members":{}}}`),
	})
	assertInvalidSpec(t, err, "billing", "/{tenant}/")

	if err := validateSpec("billing", Spec{
		Endpoint: "public", Prefix: "api", Version: "v1", Scope: ScopeTenants,
		Document: []byte(`{"openapi":"3.1.0","paths":{"/{tenant}/members":{}}}`),
	}); err != nil {
		t.Fatalf("a tenant-scoped fragment was refused: %v", err)
	}
	if err := validateSpec("billing", Spec{
		Endpoint: "public", Prefix: "api", Version: "v1", Scope: ScopeTenants,
		Document: []byte(`{"openapi":"3.1.0","paths":{"/{tenant}":{}}}`),
	}); err != nil {
		t.Fatalf("a tenants-scope operation on the tenant resource itself was refused: %v", err)
	}
	if got := (Spec{Prefix: "api", Version: "v1", Scope: ScopeTenants}).ResolvePath("/{tenant}/members"); got != "/api/v1/tenants/{tenant}/members" {
		t.Errorf("ResolvePath under the tenant scope is %q", got)
	}
}

func assertInvalidSpec(t *testing.T, err error, module, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("an unusable fragment (%s) was accepted", reason)
	}
	if !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("the failure for %s does not match ErrInvalidSpec: %v", reason, err)
	}
	if !strings.Contains(err.Error(), module) {
		t.Errorf("the failure %q does not name the declaring module %q", err, module)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("the failure %q does not say what is wrong (%q)", err, reason)
	}
}
