package selfcreate

import (
	"bytes"
	"os"
	"testing"

	"github.com/vislake/speed/pkg/internal/openapigen"
)

const fragmentPath = "openapi/tenant.selfcreate.json"

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
			"UPDATE_FRAGMENTS=1 go -C pkg test ./tenant/selfcreate -run TestTheFragmentIsGeneratedFromTheDeclarations")
	}
}
