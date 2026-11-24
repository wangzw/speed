package gate

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// forbidden are the packages the gateway's production closure may not hold: it
// keeps no store, knows no tenant directory and reaches no credential backend.
// The rule is over the closure depsOf lists; a test import is not an edge of it.
var forbidden = []string{
	"github.com/vislake/speed/pkg/authn/static",
	"github.com/vislake/speed/pkg/db",
	"github.com/vislake/speed/pkg/kv",
	"github.com/vislake/speed/pkg/event",
	"github.com/vislake/speed/pkg/settings",
	"github.com/vislake/speed/pkg/tenant",
}

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

// TestTheGatewayHoldsNoStoreAndNoTenantInItsClosure checks the closure, not only
// the direct imports, for a store, the tenant module and the static backend.
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
