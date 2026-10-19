package admin

import (
	"bytes"
	"os"
	"testing"

	"github.com/vislake/speed/pkg/internal/openapigen"
)

// fragmentPath is this module's fragment, relative to the package directory
// `go test` runs in.
const fragmentPath = "openapi/audit.admin.json"

// TestTheFragmentIsGeneratedFromTheDeclarations holds the embedded fragment to
// what the declarations generate. The fragment is a build product: a handler or
// a route that changes without the declaration fails here, and the fix is to
// change the declaration and regenerate.
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
			"UPDATE_FRAGMENTS=1 go -C pkg test ./audit/admin -run TestTheFragmentIsGeneratedFromTheDeclarations")
	}
}
