package admission

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const (
	repo        = "github.com/vislake/speed/pkg/"
	authnRoot   = repo + "authn"
	tenantRoot  = repo + "tenant"
	authnPrefix = authnRoot + "/"
)

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %v: %v\n%s", args, err, stderr.String())
	}
	return strings.Fields(string(out))
}

// TestTheLayerReachesTheIdentitySystemThroughItsRootPackageAlone checks that the
// layer imports only the authn root package and no implementation subpackage.
func TestTheLayerReachesTheIdentitySystemThroughItsRootPackageAlone(t *testing.T) {
	direct := goList(t, "-f", `{{join .Imports "\n"}}`, ".")
	for _, imp := range direct {
		if strings.HasPrefix(imp, authnPrefix) {
			t.Errorf("the layer imports %s: only %s is its way to the identity system", imp, authnRoot)
		}
	}
	if !slices.Contains(direct, authnRoot) {
		t.Fatalf("the layer does not import %s, so the check above judged nothing", authnRoot)
	}

	closure := goList(t, "-deps", "-f", "{{.ImportPath}}", ".")
	for _, pkg := range closure {
		switch {
		case !strings.HasPrefix(pkg, authnPrefix):
		case strings.HasPrefix(pkg, authnPrefix+"internal/"):
		default:
			t.Errorf("the closure holds %s", pkg)
		}
	}
}

// TestTheLayerKeepsToTheLibrariesTheDesignNames holds that the layer has no direct
// import of the database, the key-value primitives or a sibling tenant module.
func TestTheLayerKeepsToTheLibrariesTheDesignNames(t *testing.T) {
	for _, imp := range goList(t, "-f", `{{join .Imports "\n"}}`, ".") {
		switch {
		case imp == repo+"db" || strings.HasPrefix(imp, repo+"db/"),
			imp == repo+"kv" || strings.HasPrefix(imp, repo+"kv/"),
			strings.HasPrefix(imp, tenantRoot+"/") && imp != tenantRoot+"/internal/tenantctx":
			t.Errorf("the layer imports %s", imp)
		}
	}
}

// TestNoPackageOfTheAuthnTreeImportsTheLayer holds the other direction of the same
// edge: the layer is in the tenant directory and the authn tree does not reach it.
func TestNoPackageOfTheAuthnTreeImportsTheLayer(t *testing.T) {
	self := goList(t, "-f", "{{.ImportPath}}", ".")[0]
	for _, pkg := range []string{authnRoot, authnPrefix + "gate", authnPrefix + "static"} {
		if slices.Contains(goList(t, "-deps", "-f", "{{.ImportPath}}", pkg), self) {
			t.Errorf("%s depends on the admission layer", pkg)
		}
	}
}
