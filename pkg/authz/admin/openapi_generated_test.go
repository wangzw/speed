package admin

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/vislake/speed/pkg/internal/openapigen"
)

// fragmentPath is this module's fragment, relative to the package directory
// `go test` runs in.
const fragmentPath = "openapi/authz.admin.json"

// TestTheFragmentIsGeneratedFromTheDeclarations holds the embedded fragment to
// what the declarations generate. The fragment is a build product: a handler
// or a route that changes without the declaration fails here, and the fix is
// to change the declaration and regenerate.
//
// Run with UPDATE_FRAGMENTS=1 to write the generated fragment.
func TestTheFragmentIsGeneratedFromTheDeclarations(t *testing.T) {
	generated, err := openapigen.Generate(fragmentTitle, fragmentVersion, operations)
	if err != nil {
		t.Fatalf("generating the fragment: %v", err)
	}
	if os.Getenv("UPDATE_FRAGMENTS") == "1" {
		if writeErr := os.WriteFile(fragmentPath, generated, 0o644); writeErr != nil {
			t.Fatalf("writing %s: %v", fragmentPath, writeErr)
		}
	}
	committed, err := os.ReadFile(fragmentPath)
	if err != nil {
		t.Fatalf("reading %s: %v", fragmentPath, err)
	}
	if !bytes.Equal(generated, committed) {
		t.Errorf("the committed fragment is out of step with the declarations; regenerate it with " +
			"UPDATE_FRAGMENTS=1 go -C pkg test ./authz/admin -run TestTheFragmentIsGeneratedFromTheDeclarations")
	}
}

// TestTheRoleWriteBodySchemaCarriesItsDescription holds the published schema
// against the hand-written fragment it replaced: the role write's body keeps its
// description, and the object stays open because the mounted route drops the
// members the declaration does not describe rather than refusing them —
// role_test.go sends a builtin member and holds that it reaches nothing. Neither
// property is visible in the generation check, which compares the fragment with
// a declaration that could have lost them.
func TestTheRoleWriteBodySchemaCarriesItsDescription(t *testing.T) {
	generated, err := openapigen.Generate(fragmentTitle, fragmentVersion, operations)
	if err != nil {
		t.Fatalf("generating the fragment: %v", err)
	}
	var fragment struct {
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(generated, &fragment); err != nil {
		t.Fatalf("decoding the fragment: %v", err)
	}
	schema, ok := fragment.Components.Schemas["AuthzAdminRoleWrite"]
	if !ok {
		t.Fatalf("the fragment carries no AuthzAdminRoleWrite schema")
	}
	const description = "The body of a role write: the role's description. " +
		"The builtin mark is not part of the body; a member sent under that name reaches nothing."
	if schema["description"] != description {
		t.Errorf("the write body schema description reads %v, want the declared one", schema["description"])
	}
	if open, ok := schema["additionalProperties"].(bool); !ok || !open {
		t.Errorf("the write body schema reads %v, want additionalProperties true: the route drops a member the declaration does not describe", schema)
	}
}
