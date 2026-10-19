package admin

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/openapigen"
)

// The module's two fragments, relative to the package directory `go test` runs
// in: the platform-domain surface and the tenant-domain one.
const (
	systemFragmentPath  = "openapi/admin.json"
	tenantsFragmentPath = "openapi/admin.tenants.json"
)

// TestTheFragmentIsGeneratedFromTheDeclarations holds the embedded fragments to
// what the declarations generate. A fragment is a build product: a handler or a
// route that changes without the declaration fails here, and the fix is to
// change the declaration and regenerate.
//
// Run with UPDATE_FRAGMENTS=1 to write the generated fragments.
func TestTheFragmentIsGeneratedFromTheDeclarations(t *testing.T) {
	for _, fragment := range []struct {
		path       string
		operations speedhttp.Operations
	}{
		{systemFragmentPath, systemOperations},
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
				"with UPDATE_FRAGMENTS=1 go -C pkg test ./authn/admin "+
				"-run TestTheFragmentIsGeneratedFromTheDeclarations", fragment.path)
		}
	}
}

// TestTheProviderWriteConfigIsPublishedAsAnObject holds the schema of a
// provider write's config: the capability accepts only a JSON object, so the
// fragment publishes that constraint rather than leaving the member open to any
// value.
func TestTheProviderWriteConfigIsPublishedAsAnObject(t *testing.T) {
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Type string `json:"type"`
				} `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(adminTenantsFragment, &doc); err != nil {
		t.Fatalf("the embedded OpenAPI fragment is not a JSON object: %v", err)
	}
	schema, ok := doc.Components.Schemas["AuthnAdminProviderWrite"]
	if !ok {
		t.Fatal("the fragment declares no AuthnAdminProviderWrite schema")
	}
	config, ok := schema.Properties["config"]
	if !ok {
		t.Fatal("the AuthnAdminProviderWrite schema carries no config member")
	}
	if config.Type != "object" {
		t.Errorf("the config member's schema type = %q, want object", config.Type)
	}
}
