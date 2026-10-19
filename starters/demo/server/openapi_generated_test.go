package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/vislake/speed/pkg/http/openapigen"
)

// personalFragmentPath is this module's fragment, relative to the package
// directory `go test` runs in. It is the file personalFragment embeds.
const personalFragmentPath = "personal-openapi.json"

// TestTheFragmentIsGeneratedFromTheDeclarations holds the committed fragment to
// what the declarations generate. The fragment is a build product: an operation
// or a reply type that changes without the declaration fails here, and the fix
// is to change the declaration and regenerate.
//
// The host's declaration lives in this module rather than in a pkg package, so
// the generator is reached through pkg/http/openapigen, its importable entry
// point; the implementation is the one every pkg module's generation test runs.
//
// Run with UPDATE_FRAGMENTS=1 to write the generated fragment.
func TestTheFragmentIsGeneratedFromTheDeclarations(t *testing.T) {
	generated, err := openapigen.Generate(personalFragmentTitle, personalFragmentVersion, personalOperations)
	if err != nil {
		t.Fatalf("generating the fragment: %v", err)
	}
	if os.Getenv("UPDATE_FRAGMENTS") == "1" {
		if writeErr := os.WriteFile(personalFragmentPath, generated, 0o644); writeErr != nil {
			t.Fatalf("writing %s: %v", personalFragmentPath, writeErr)
		}
	}
	committed, err := os.ReadFile(personalFragmentPath)
	if err != nil {
		t.Fatalf("reading %s: %v", personalFragmentPath, err)
	}
	if !bytes.Equal(generated, committed) {
		t.Errorf("the committed fragment is out of step with the declarations; regenerate it with " +
			"UPDATE_FRAGMENTS=1 go -C starters/demo/server test . -run TestTheFragmentIsGeneratedFromTheDeclarations")
	}
}
