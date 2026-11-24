package invite

import (
	"bytes"
	"os"
	"testing"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/openapigen"
)

const (
	publicFragmentPath  = "openapi/invite.json"
	tenantsFragmentPath = "openapi/invite.tenants.json"
)

// TestTheFragmentIsGeneratedFromTheDeclarations checks that the committed
// fragments match what the declarations generate.
func TestTheFragmentIsGeneratedFromTheDeclarations(t *testing.T) {
	for _, fragment := range []struct {
		path       string
		operations speedhttp.Operations
	}{
		{publicFragmentPath, publicOperations},
		{tenantsFragmentPath, tenantOperations},
	} {
		generated, err := openapigen.Generate(fragmentTitle, fragmentVersion, fragment.operations)
		if err != nil {
			t.Fatalf("generating %s: %v", fragment.path, err)
		}
		if os.Getenv("UPDATE_FRAGMENTS") == "1" {
			if writeErr := os.WriteFile(fragment.path, generated, 0o644); writeErr != nil {
				t.Fatalf("writing %s: %v", fragment.path, writeErr)
			}
		}
		committed, err := os.ReadFile(fragment.path)
		if err != nil {
			t.Fatalf("reading %s: %v", fragment.path, err)
		}
		if !bytes.Equal(generated, committed) {
			t.Errorf("the committed fragment %s is out of step with the declarations; regenerate it "+
				"with UPDATE_FRAGMENTS=1 go -C pkg test ./authn/invite "+
				"-run TestTheFragmentIsGeneratedFromTheDeclarations", fragment.path)
		}
	}
}
