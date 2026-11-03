package tenantadmin

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
	tenantAdminPath  = moduleImportPath + "/authz/tenantadmin"
	rootPath         = moduleImportPath + "/authz"
	internalPath     = rootPath + "/internal/"
	authztestPath    = rootPath + "/authztest"
	guardPath        = rootPath + "/guard"
	policyPath       = rootPath + "/policy"
	dbPath           = moduleImportPath + "/db"
	ginPath          = "github.com/gin-gonic/gin"
)

// listed is what these checks read about the tenantadmin package.
type listed struct {
	ImportPath string
	Imports    []string
}

// tenantAdminImports lists the package with go list and returns the imports of
// its non-test files.
func tenantAdminImports(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-e", "-json=ImportPath,Imports", tenantAdminPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", tenantAdminPath, err, stderr.String())
	}
	var p listed
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("decoding go list output: %v", err)
	}
	if p.ImportPath != tenantAdminPath {
		t.Fatalf("go list reported %q, want %q", p.ImportPath, tenantAdminPath)
	}
	return p.Imports
}

// requiredImports are the packages the tenant management module takes. The
// Manager, Catalog, SubjectPermissions, MembershipChecker, WriteGuard and the
// record types live in the root package; the handler-internal decision API,
// Guard, lives in the guard subpackage. The rest are the modules it consumes:
// gin for the handlers, and the authn, http, audit, core, i18n and log modules.
var requiredImports = []string{
	rootPath,
	guardPath,
	moduleImportPath + "/authn",
	moduleImportPath + "/http",
	moduleImportPath + "/audit",
	moduleImportPath + "/core",
	moduleImportPath + "/i18n",
	moduleImportPath + "/log",
	ginPath,
}

// missingImports is the presence rule.
func missingImports(imports []string) []string {
	var out []string
	for _, want := range requiredImports {
		if slices.Contains(imports, want) {
			continue
		}
		out = append(out, tenantAdminPath+" does not import "+want+
			": the tenant management module takes the interface packages and the modules it consumes")
	}
	slices.Sort(out)
	return out
}

// forbiddenImports is the exclusion rule: the package imports neither the
// policy subpackage (nor, through it, Casbin or gorm), nor the internal library
// packages that hold the engine and its store, nor the db module. Reaching the
// implementation would put the engine and its store into a command that only
// mounts the tenant management routes.
func forbiddenImports(imports []string) []string {
	var out []string
	for _, imp := range imports {
		switch {
		case imp == policyPath,
			strings.HasPrefix(imp, internalPath),
			imp == authztestPath,
			strings.HasPrefix(imp, "github.com/casbin/"),
			strings.HasPrefix(imp, "gorm.io/"),
			imp == dbPath:
			out = append(out, tenantAdminPath+" imports "+imp+
				": the tenant management module imports the interface, not the policy implementation")
		}
	}
	slices.Sort(out)
	return out
}

// TestTheTenantAdminImportsTheInterfaceNotTheImplementation is the executable
// form of the packaging decision's dependency statement for this module.
func TestTheTenantAdminImportsTheInterfaceNotTheImplementation(t *testing.T) {
	imports := tenantAdminImports(t)
	for _, v := range missingImports(imports) {
		t.Error(v)
	}
	for _, v := range forbiddenImports(imports) {
		t.Error(v)
	}
}

// TestTheListingReadsTheModule is the positive control.
func TestTheListingReadsTheModule(t *testing.T) {
	imports := tenantAdminImports(t)
	if len(imports) == 0 {
		t.Fatal("the tenantadmin package lists no import, so the listing is not reading its files")
	}
	if !slices.Contains(imports, ginPath) {
		t.Fatalf("the tenantadmin package does not list the gin import, so the listing is not reading the handlers: %v", imports)
	}
	if !slices.Contains(imports, rootPath) {
		t.Fatalf("the tenantadmin package does not list the root package, so what is read is not this module: %v", imports)
	}
}

// TestTheBoundaryRulesFailOnAViolation holds each rule to be a rule.
func TestTheBoundaryRulesFailOnAViolation(t *testing.T) {
	if got := missingImports(slices.Clone(requiredImports)); len(got) != 0 {
		t.Errorf("the presence rule reports %q on the full set, want none", got)
	}
	short := slices.DeleteFunc(slices.Clone(requiredImports), func(s string) bool { return s == guardPath })
	if got := missingImports(short); !slices.Equal(got, []string{
		tenantAdminPath + " does not import " + guardPath +
			": the tenant management module takes the interface packages and the modules it consumes",
	}) {
		t.Errorf("the presence rule reports %q, want the one missing import", got)
	}

	bad := []string{
		rootPath, policyPath, internalPath + "persist", internalPath + "policy", authztestPath,
		"github.com/casbin/casbin/v3", "gorm.io/gorm", dbPath,
	}
	want := []string{
		tenantAdminPath + " imports " + dbPath + ": the tenant management module imports the interface, not the policy implementation",
		tenantAdminPath + " imports " + policyPath + ": the tenant management module imports the interface, not the policy implementation",
		tenantAdminPath + " imports " + internalPath + "persist: the tenant management module imports the interface, not the policy implementation",
		tenantAdminPath + " imports " + internalPath + "policy: the tenant management module imports the interface, not the policy implementation",
		tenantAdminPath + " imports " + authztestPath + ": the tenant management module imports the interface, not the policy implementation",
		tenantAdminPath + " imports github.com/casbin/casbin/v3: the tenant management module imports the interface, not the policy implementation",
		tenantAdminPath + " imports gorm.io/gorm: the tenant management module imports the interface, not the policy implementation",
	}
	slices.Sort(want)
	if got := forbiddenImports(bad); !slices.Equal(got, want) {
		t.Errorf("the forbidden rule reports %q, want the forbidden entries %q", got, want)
	}
	if got := forbiddenImports([]string{ginPath, "context", rootPath}); len(got) != 0 {
		t.Errorf("the forbidden rule reports %q on gin and a standard-library import, want none", got)
	}
}
