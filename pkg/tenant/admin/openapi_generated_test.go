package admin

import (
	"bytes"
	"os"
	"testing"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/openapigen"
)

const fragmentPath = "openapi/tenant.admin.json"

const tenantOwnerFragmentPath = "openapi/tenant.owner.json"

const selfFragmentPath = "openapi/tenant.self.json"

// TestTheFragmentIsGeneratedFromTheDeclarations holds the embedded fragments to what
// the declarations generate.
func TestTheFragmentIsGeneratedFromTheDeclarations(t *testing.T) {
	for _, c := range []struct {
		path  string
		title string
		ops   speedhttp.Operations
	}{
		{fragmentPath, fragmentTitle, operations},
		{tenantOwnerFragmentPath, tenantOwnerFragmentTitle, tenantOperations},
		{selfFragmentPath, selfFragmentTitle, selfOperations},
	} {
		generated, err := openapigen.Generate(c.title, fragmentVersion, c.ops)
		if err != nil {
			t.Fatalf("generating %s: %v", c.path, err)
		}
		if os.Getenv("UPDATE_FRAGMENTS") == "1" {
			if writeErr := os.WriteFile(c.path, generated, 0o644); writeErr != nil {
				t.Fatalf("writing %s: %v", c.path, writeErr)
			}
		}
		committed, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("reading %s: %v", c.path, err)
		}
		if !bytes.Equal(generated, committed) {
			t.Errorf("the committed fragment %s is out of step with the declarations; regenerate it with "+
				"UPDATE_FRAGMENTS=1 go -C pkg test ./tenant/admin -run TestTheFragmentIsGeneratedFromTheDeclarations",
				c.path)
		}
	}
}
