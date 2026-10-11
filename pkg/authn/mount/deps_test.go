package mount

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// forbidden are the packages this module's closure may not hold. It keeps no
// store, takes no part in settings or events, and does not know the tenant
// directory: tenancy depends on authentication and not the other way round. It
// does not import the gateway either: the gateway is reached through the
// interface, so that a host's own gateway takes this module in, and importing
// its package would register the gateway module with every binary that imports
// this one.
var forbidden = []string{
	"github.com/vislake/speed/pkg/authn/gate",
	"github.com/vislake/speed/pkg/authn/static",
	"github.com/vislake/speed/pkg/db",
	"github.com/vislake/speed/pkg/kv",
	"github.com/vislake/speed/pkg/event",
	"github.com/vislake/speed/pkg/settings",
	"github.com/vislake/speed/pkg/tenant",
}

// depsOf is every package in the closure of this package's non-test files.
func depsOf(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("listing the closure of the module: %v\n%s", err, stderr.String())
	}
	return strings.Fields(string(out))
}

// forbiddenIn reports the packages of the listing that are, or sit under, one of
// the forbidden ones.
func forbiddenIn(listing []string) []string {
	var out []string
	for _, pkg := range listing {
		for _, f := range forbidden {
			if pkg == f || strings.HasPrefix(pkg, f+"/") {
				out = append(out, pkg)
			}
		}
	}
	return out
}

// TestTheModuleHoldsNoStoreNoGatewayAndNoTenantInItsClosure holds the dependency
// statement over the closure and not only over the direct imports. The controls
// make it more than an empty listing: the closure has to hold what the module
// uses, and the reading has to find a forbidden package under its own path and
// under a subpackage.
func TestTheModuleHoldsNoStoreNoGatewayAndNoTenantInItsClosure(t *testing.T) {
	listing := depsOf(t)
	for _, want := range []string{
		"github.com/vislake/speed/pkg/authn", "github.com/vislake/speed/pkg/authn/internal/mount",
		"github.com/vislake/speed/pkg/http", "github.com/vislake/speed/pkg/core",
	} {
		found := false
		for _, pkg := range listing {
			found = found || pkg == want
		}
		if !found {
			t.Fatalf("the closure does not hold %s, so it is not this module's", want)
		}
	}
	if got := forbiddenIn(listing); len(got) != 0 {
		t.Errorf("the module's closure holds %v, which it may not", got)
	}

	control := forbiddenIn([]string{
		"fmt", "github.com/vislake/speed/pkg/db", "github.com/vislake/speed/pkg/tenant/resolve",
		"github.com/vislake/speed/pkg/dbx", "github.com/vislake/speed/pkg/authn/gate",
		"github.com/vislake/speed/pkg/authn/gatex",
	})
	if len(control) != 3 {
		t.Errorf("the reading found %v in a listing with a store, a tenant subpackage and the gateway, want exactly those three", control)
	}
}
