package admin

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

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

type listed struct {
	ImportPath string
	// Imports are the imports of the package's non-test files that build here.
	Imports []string
}

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

// TestTheAdminImportsTheInterfaceNotTheImplementation checks that the admin imports the interface not the implementation.
func TestTheAdminImportsTheInterfaceNotTheImplementation(t *testing.T) {
	imports := adminImports(t)
	for _, v := range missingImports(imports) {
		t.Error(v)
	}
	for _, v := range forbiddenImports(imports) {
		t.Error(v)
	}
}

// TestTheListingReadsTheModule checks that the listing reads the module.
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

// TestTheBoundaryRulesFailOnAViolation checks that the boundary rules fail on a violation.
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
