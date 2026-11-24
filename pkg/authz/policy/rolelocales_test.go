package policy

import (
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"testing"
)

// TestTheEmbeddedRoleCatalogCarriesOneKeySetForEveryLocale holds the role
// catalog to the two builtin description keys the host seeds, in every
// locale: one missing entry would render a builtin role as its key, and an
// extra entry is text nothing can ask for.
func TestTheEmbeddedRoleCatalogCarriesOneKeySetForEveryLocale(t *testing.T) {
	want := []string{
		"authz.role.platformAdmin",
		"authz.role.tenantAdmin",
	}
	names, err := fs.Glob(roleLocalesFS, roleMessageCatalog.Pattern)
	if err != nil {
		t.Fatalf("the catalog pattern %q is malformed: %v", roleMessageCatalog.Pattern, err)
	}
	if len(names) < 2 {
		t.Fatalf("the catalog pattern %q matched %d file(s): a key set compared against one "+
			"language says nothing about the other", roleMessageCatalog.Pattern, len(names))
	}
	var first map[string]string
	firstFile := ""
	for _, name := range names {
		raw, err := fs.ReadFile(roleLocalesFS, name)
		if err != nil {
			t.Fatalf("reading the embedded %s: %v", name, err)
		}
		var entries map[string]string
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("%s is not a JSON object of message text: %v", name, err)
		}
		keys := make([]string, 0, len(entries))
		for key, text := range entries {
			keys = append(keys, key)
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s carries %q with no text behind it", name, key)
			}
		}
		slices.Sort(keys)
		if !slices.Equal(keys, want) {
			t.Errorf("%s carries %v, want the builtin role keys %v", name, keys, want)
		}
		if first == nil {
			first, firstFile = entries, name
			continue
		}
		if len(entries) != len(first) {
			t.Errorf("%s carries %d key(s) and %s carries %d", name, len(entries), firstFile, len(first))
		}
		for key := range first {
			if _, ok := entries[key]; !ok {
				t.Errorf("%s carries no entry for %q, which %s carries", name, key, firstFile)
			}
		}
	}
}
