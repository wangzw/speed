package authztest_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const (
	authzRootPath = "github.com/vislake/speed/pkg/authz"
	policyPath    = authzRootPath + "/policy"
	guardPath     = authzRootPath + "/guard"
)

// TestAuthztestImportsTheRootPackageAlone checks that authztest imports the root package alone.
func TestAuthztestImportsTheRootPackageAlone(t *testing.T) {
	imports := packageImports(t, ".")
	if !contains(imports, authzRootPath) {
		t.Fatalf("go list does not report authztest importing %s, so it is not reading this package: %v",
			authzRootPath, imports)
	}
	for _, imp := range subpackageViolations(imports) {
		t.Errorf("authztest imports %s: it may import the root package and never a subpackage that imports "+
			"it back from a test", imp)
	}
}

// TestTheSubpackageRuleFailsOnAForbiddenImport checks that the subpackage rule fails on a forbidden import.
func TestTheSubpackageRuleFailsOnAForbiddenImport(t *testing.T) {
	got := subpackageViolations([]string{authzRootPath, policyPath, guardPath + "/internal", "context"})
	want := []string{policyPath, guardPath + "/internal"}
	if len(got) != len(want) {
		t.Fatalf("violations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("violation %d is %q, want %q", i, got[i], want[i])
		}
	}
	if v := subpackageViolations([]string{authzRootPath, "github.com/vislake/speed/pkg/core"}); len(v) != 0 {
		t.Errorf("the rule reports %v for imports it allows", v)
	}
}

func subpackageViolations(imports []string) []string {
	var out []string
	for _, imp := range imports {
		for _, forbidden := range []string{policyPath, guardPath} {
			if imp == forbidden || strings.HasPrefix(imp, forbidden+"/") {
				out = append(out, imp)
				break
			}
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

type listed struct {
	ImportPath string
	// Imports are the imports of the package's non-test files. go list reports
	// the test files' imports separately, and the rules above are about
	// production code, so those are not asked for.
	Imports []string
}

func packageImports(t *testing.T, pattern string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-e", "-json=ImportPath,Imports", pattern)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v", pattern, err)
	}
	var p listed
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("decoding go list output for %s: %v", pattern, err)
	}
	if p.ImportPath == "" {
		t.Fatalf("go list %s returned no package: %s", pattern, out)
	}
	if p.ImportPath != authzRootPath+"/authztest" {
		t.Fatalf("go list %s listed %s, want this package %s", pattern, p.ImportPath, authzRootPath+"/authztest")
	}
	return p.Imports
}
