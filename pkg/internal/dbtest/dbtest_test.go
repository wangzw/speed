package dbtest

import (
	"net/url"
	"testing"
)

// TestSQLiteInADirectoryNameWithURLMetacharacters reproduces the failure a
// locator built by concatenation has. testing.TempDir embeds the subtest name in
// the directory, and two subtests of one name make it append "#01"; a locator
// written "file://"+path then loses everything from the "#" on, and the loader
// sees a directory with no extension instead of the config file. The two
// subtests share a name on purpose: that is what puts the metacharacter in the
// path.
func TestSQLiteInADirectoryNameWithURLMetacharacters(t *testing.T) {
	for i := 0; i < 2; i++ {
		t.Run("same", func(t *testing.T) {
			if db := SQLite(t, ":memory:", nil); db == nil {
				t.Fatal("the fixture delivered no handle")
			}
		})
	}
}

// TestTheDefaultLocatorSurvivesURLMetacharacters pins the round trip directly:
// whatever a directory name carries, the locator has to read back as the same
// path, because the loader takes the path from the parsed URL.
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
