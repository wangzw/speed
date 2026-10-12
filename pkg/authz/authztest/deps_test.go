package authztest_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// The import paths the rule below is about: the root package authztest builds
// its stubs against, and the two subpackages that import authztest from their
// own tests.
const (
	authzRootPath = "github.com/vislake/speed/pkg/authz"
	policyPath    = authzRootPath + "/policy"
	guardPath     = authzRootPath + "/guard"
)

// TestAuthztestImportsTheRootPackageAlone is the executable form of the
// packaging limit: authztest's non-test imports are the packages the fixture
// needs and never the policy or enforcement subpackage. Those two import
// authztest from their tests, so an import back would be a cycle; the stubs
// exist so that it is never needed.
//
// The listing is go list's, read from the package's own directory, and a
// positive control holds that it is reading this package: a listing that came
// back empty would let the rule pass while looking at nothing.
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

// TestTheSubpackageRuleFailsOnAForbiddenImport holds the rule to be a rule and
// not a check that accepts anything: an import of the policy or the enforcement
// subpackage, or of a package under either, is reported, while an import of the
// root package or of another package the fixture uses is not.
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

// subpackageViolations reports the imports that name the policy or enforcement
// subpackage, or a package under either.
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

// listed is what this check reads of go list's output for one package.
type listed struct {
	ImportPath string
	// Imports are the imports of the package's non-test files. go list reports
	// the test files' imports separately, and the rules above are about
	// production code, so those are not asked for.
	Imports []string
}

// packageImports lists a package with go list and returns its non-test imports.
// The package's own directory is the working directory go test runs this file
// from, so a relative pattern reaches this package.
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
