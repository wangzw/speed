package dbtest

import (
	"net/url"
	"testing"
)

// TestSQLiteInADirectoryNameWithURLMetacharacters reproduces the failure a locator built by
// concatenation has.
func TestSQLiteInADirectoryNameWithURLMetacharacters(t *testing.T) {
	for i := 0; i < 2; i++ {
		t.Run("same", func(t *testing.T) {
			if db := SQLite(t, ":memory:", nil); db == nil {
				t.Fatal("the fixture delivered no handle")
			}
		})
	}
}

// TestTheDefaultLocatorSurvivesURLMetacharacters checks that a file path containing #, ? or a space
// reads back unchanged from its default locator.
func TestTheDefaultLocatorSurvivesURLMetacharacters(t *testing.T) {
	for _, path := range []string{
		"/tmp/a#1/config.json",
		"/tmp/a?b/config.json",
		"/tmp/a b/config.json",
	} {
		parsed, err := url.Parse(defaultLocator(path))
		if err != nil {
			t.Fatalf("parsing the locator for %q: %v", path, err)
		}
		if parsed.Path != path {
			t.Errorf("the locator for %q reads back as %q", path, parsed.Path)
		}
	}
}
