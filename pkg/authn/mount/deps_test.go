package mount

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

var forbidden = []string{
	"github.com/vislake/speed/pkg/authn/gate",
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
		t.Fatalf("listing the closure of the module: %v\n%s", err, stderr.String())
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

// TestTheModuleHoldsNoStoreNoGatewayAndNoTenantInItsClosure holds the dependency statement over the whole closure.
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
