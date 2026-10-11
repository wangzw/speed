package gate

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// forbidden are the packages the gateway's closure may not hold: it keeps no
// store, and it does not know the tenant directory. A gateway that depended on
// the database or the key-value primitives would stop being the layer that
// answers when the store is down, and one that imported the tenant module would
// reverse the direction the design fixes between authentication and tenancy.
//
// The first credential backend, pkg/authn/static, is held out as well: the
// gateway knows backends only through the interface and the backend does not
// import the gateway, so neither reaches the other. The rule is about this
// package's production closure, which is what depsOf lists; the external test
// package imports the backend to run the two together, and a test import is not
// an edge of the closure.
var forbidden = []string{
	"github.com/vislake/speed/pkg/authn/static",
	"github.com/vislake/speed/pkg/db",
	"github.com/vislake/speed/pkg/kv",
	"github.com/vislake/speed/pkg/event",
	"github.com/vislake/speed/pkg/settings",
	"github.com/vislake/speed/pkg/tenant",
}

// depsOf is every package in the closure of this package's non-test files, one
// import path a line.
func depsOf(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("listing the closure of the gateway: %v\n%s", err, stderr.String())
	}
	return strings.Fields(string(out))
}

// forbiddenIn reports the packages of the listing that are, or sit under, one of the
// forbidden ones.
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

// TestTheGatewayHoldsNoStoreAndNoTenantInItsClosure holds the dependency
// statement of the design over the closure and not only over the direct
// imports: something it imports for another reason could bring a store in.
//
// The controls make the case more than an empty listing: the closure has to hold
// the packages the gateway is known to use, and the reading has to find a
// forbidden package in a listing that has one, under its own path and under a
// subpackage.
func TestTheGatewayHoldsNoStoreAndNoTenantInItsClosure(t *testing.T) {
	listing := depsOf(t)

	for _, want := range []string{"github.com/vislake/speed/pkg/authn", "github.com/vislake/speed/pkg/http", "github.com/vislake/speed/pkg/core"} {
		found := false
		for _, pkg := range listing {
			found = found || pkg == want
		}
		if !found {
			t.Fatalf("the closure does not hold %s, so it is not the gateway's", want)
		}
	}
	if got := forbiddenIn(listing); len(got) != 0 {
		t.Errorf("the gateway's closure holds %v, which it may not", got)
	}

	control := forbiddenIn([]string{
		"fmt", "github.com/vislake/speed/pkg/db", "github.com/vislake/speed/pkg/tenant/resolve",
		"github.com/vislake/speed/pkg/dbx", "github.com/vislake/speed/pkg/authn/static",
		"github.com/vislake/speed/pkg/authn/staticx",
	})
	if len(control) != 3 {
		t.Errorf("the reading found %v in a listing with a store, a tenant subpackage and the static backend, want exactly those three", control)
	}
}
