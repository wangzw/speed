package treecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vislake/speed/starters/demo/server/internal/demotest"
)

// backendPrefixManifestPath is the dev-proxy manifest the frontend package
// carries, relative to this package's directory.
const backendPrefixManifestPath = "../../web/backend-prefixes.json"

// readPrefixManifest decodes that manifest into one prefix-to-endpoint table.
// The file groups prefixes by the endpoint that claims them, which is the shape
// the dev proxy and the frontend's route guard read; the comparison works on
// the flat table, because that is the claim the configuration makes.
func readPrefixManifest(t *testing.T) map[string]string {
	t.Helper()
	doc, err := os.ReadFile(filepath.Clean(backendPrefixManifestPath))
	if err != nil {
		t.Fatalf("reading %s: %v", backendPrefixManifestPath, err)
	}
	var parsed map[string][]string
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("%s is not a JSON object of endpoint to prefixes: %v", backendPrefixManifestPath, err)
	}
	if len(parsed) == 0 {
		t.Fatalf("%s lists no endpoint", backendPrefixManifestPath)
	}
	owners := make(map[string]string)
	for endpoint, prefixes := range parsed {
		for _, prefix := range prefixes {
			if prefix == "/" {
				t.Errorf("%s: %q lists the root, which the web fallback endpoint claims and no API namespace owns",
					backendPrefixManifestPath, endpoint)
				continue
			}
			if other, duplicate := owners[prefix]; duplicate {
				t.Errorf("%s: both %q and %q list %q", backendPrefixManifestPath, other, endpoint, prefix)
				continue
			}
			owners[prefix] = endpoint
		}
	}
	return owners
}

// TestThePrefixManifestMatchesTheOwnershipTable checks that the frontend's
// prefix manifest and the ownership table give each prefix the same endpoint,
// in both directions.
func TestThePrefixManifestMatchesTheOwnershipTable(t *testing.T) {
	t.Parallel()
	owners := readPrefixManifest(t)
	expectedOwners := demotest.ExpectedOwners()
	for prefix, endpoint := range owners {
		expected, listed := expectedOwners[prefix]
		switch {
		case !listed:
			t.Errorf("%s: %q lists %q, which the ownership table gives to no endpoint",
				backendPrefixManifestPath, endpoint, prefix)
		case expected != endpoint:
			t.Errorf("%s: %q lists %q, but the ownership table gives it to %q",
				backendPrefixManifestPath, endpoint, prefix, expected)
		}
	}
	for prefix, endpoint := range expectedOwners {
		if _, listed := owners[prefix]; !listed {
			t.Errorf("%s: no endpoint lists %q, which the ownership table gives to %q",
				backendPrefixManifestPath, prefix, endpoint)
		}
	}
}
