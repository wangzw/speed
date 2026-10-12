package admin

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The import paths the rules below are about.
const (
	moduleImportPath = "github.com/vislake/speed/pkg"
	adminPath        = moduleImportPath + "/authz/admin"
	rootPath         = moduleImportPath + "/authz"
	internalPath     = rootPath + "/internal/"
	authztestPath    = rootPath + "/authztest"
	guardPath        = rootPath + "/guard"
	policyPath       = rootPath + "/policy"
	tenantPath       = moduleImportPath + "/tenant"
	dbPath           = moduleImportPath + "/db"
	ginPath          = "github.com/gin-gonic/gin"
)

// listed is what these checks read about the admin package: its import path and
// the imports of its non-test files. go list reports the test files' imports
// separately, and the rules are about production code, so those are not asked
// for.
type listed struct {
	ImportPath string
	// Imports are the imports of the package's non-test files that build here.
	Imports []string
}

// adminImports lists the admin package with go list and returns the imports of
// its non-test files. The pattern names the package by its import path, so the
// listing does not depend on the directory the tests run from. -e keeps the
// package in the listing even when it does not compile, with its imports as the
// parser read them.
func adminImports(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-e", "-json=ImportPath,Imports", adminPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", adminPath, err, stderr.String())
	}
	var p listed
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("decoding go list output: %v", err)
	}
	if p.ImportPath != adminPath {
		t.Fatalf("go list reported %q, want %q", p.ImportPath, adminPath)
	}
	return p.Imports
}

// requiredImports are the packages the admin module's non-test code takes. The
// decision values it writes through — the Manager and the Catalog read face —
// live in the root package, and the handler-internal decision API, Guard, lives
// in the guard subpackage. The rest are the modules it consumes: gin for the
// handlers, and the authn, http, audit, core and log modules. The tenant comes
// from the route's path parameter and not from the tenant package, so that one
// is not here.
var requiredImports = []string{
	rootPath,
	guardPath,
	moduleImportPath + "/authn",
	moduleImportPath + "/http",
	moduleImportPath + "/audit",
	moduleImportPath + "/core",
	moduleImportPath + "/log",
	ginPath,
}

// missingImports is the presence rule: every required import is listed. Reading
// the decision values from the root package and the decision API from the guard
// subpackage is what keeps this module the interface's consumer and not the
// implementation's; the module imports are what make it a route surface on the
// entry point, a reader of identities and a writer of audits.
func missingImports(imports []string) []string {
	var out []string
	for _, want := range requiredImports {
		if slices.Contains(imports, want) {
			continue
		}
		out = append(out, adminPath+" does not import "+want+
			": the admin module takes the interface packages and the modules it consumes")
	}
	slices.Sort(out)
	return out
}

// forbiddenImports is the exclusion rule: the admin package imports neither the
// policy subpackage of this directory (nor, through it, Casbin or gorm), nor the
// internal library packages that hold the engine and its store, nor the db
// module, nor the tenant package. The Manager, the Catalog and the record types
// live in the root package; reaching the implementation would put the engine and
// its store into a command that only mounts the management routes, and the
// tenant comes from the route's path parameter rather than from a roster this
// module reads. gin is a leaf's to hold and the standard library is always
// allowed, so neither is reported.
func forbiddenImports(imports []string) []string {
	var out []string
	for _, imp := range imports {
		switch {
		case imp == policyPath,
			strings.HasPrefix(imp, internalPath),
			imp == authztestPath,
			strings.HasPrefix(imp, "github.com/casbin/"),
			strings.HasPrefix(imp, "gorm.io/"),
			imp == dbPath,
			imp == tenantPath:
			out = append(out, adminPath+" imports "+imp+
				": the admin module imports the interface, not the policy implementation")
		}
	}
	slices.Sort(out)
	return out
}

// TestTheAdminImportsTheInterfaceNotTheImplementation is the executable form of
// the packaging decision's dependency statement for this module: the Manager and
// the Catalog come from the root package and the decision API from the guard
// subpackage, the routes are mounted through the http module, and the policy
// subpackage, Casbin, gorm and the db module are not in the non-test imports.
// The types the routes read and write are the root package's, so a command that
// only mounts the management routes does not carry the engine or its store.
func TestTheAdminImportsTheInterfaceNotTheImplementation(t *testing.T) {
	imports := adminImports(t)
	for _, v := range missingImports(imports) {
		t.Error(v)
	}
	for _, v := range forbiddenImports(imports) {
		t.Error(v)
	}
}

// TestTheListingReadsTheModule is the positive control: a listing that came back
// empty, or that never named the module, would let the rules pass while looking
// at nothing. The gin import holds that the listing reads the handler files,
// which are the ones that bind the route surface, and the root import holds that
// what is read is this module and not another package of the same name.
func TestTheListingReadsTheModule(t *testing.T) {
	imports := adminImports(t)
	if len(imports) == 0 {
		t.Fatal("the admin package lists no import, so the listing is not reading its files")
	}
	if !slices.Contains(imports, ginPath) {
		t.Fatalf("the admin package does not list the gin import, so the listing is not reading the handlers: %v", imports)
	}
	if !slices.Contains(imports, rootPath) {
		t.Fatalf("the admin package does not list the root package, so what is read is not this module: %v", imports)
	}
}

// TestTheBoundaryRulesFailOnAViolation holds each rule to be a rule and not a
// check that accepts anything: a missing required import is reported and the
// full set is not, and a policy, Casbin, gorm or db import is reported while gin
// and a standard-library import are not.
func TestTheBoundaryRulesFailOnAViolation(t *testing.T) {
	if got := missingImports(slices.Clone(requiredImports)); len(got) != 0 {
		t.Errorf("the presence rule reports %q on the full set, want none", got)
	}
	short := slices.DeleteFunc(slices.Clone(requiredImports), func(s string) bool { return s == guardPath })
	if got := missingImports(short); !slices.Equal(got, []string{
		adminPath + " does not import " + guardPath +
			": the admin module takes the interface packages and the modules it consumes",
	}) {
		t.Errorf("the presence rule reports %q, want the one missing import", got)
	}

	bad := []string{
		rootPath, policyPath, internalPath + "persist", internalPath + "policy", authztestPath,
		"github.com/casbin/casbin/v3", "gorm.io/gorm", dbPath, tenantPath,
	}
	want := []string{
		adminPath + " imports " + dbPath + ": the admin module imports the interface, not the policy implementation",
		adminPath + " imports " + policyPath + ": the admin module imports the interface, not the policy implementation",
		adminPath + " imports " + internalPath + "persist: the admin module imports the interface, not the policy implementation",
		adminPath + " imports " + internalPath + "policy: the admin module imports the interface, not the policy implementation",
		adminPath + " imports " + authztestPath + ": the admin module imports the interface, not the policy implementation",
		adminPath + " imports " + tenantPath + ": the admin module imports the interface, not the policy implementation",
		adminPath + " imports github.com/casbin/casbin/v3: the admin module imports the interface, not the policy implementation",
		adminPath + " imports gorm.io/gorm: the admin module imports the interface, not the policy implementation",
	}
	slices.Sort(want)
	if got := forbiddenImports(bad); !slices.Equal(got, want) {
		t.Errorf("the forbidden rule reports %q, want the forbidden entries %q", got, want)
	}
	if got := forbiddenImports([]string{ginPath, "context", rootPath}); len(got) != 0 {
		t.Errorf("the forbidden rule reports %q on gin and a standard-library import, want none", got)
	}
}
