package policy

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
)

// TestTheEmbeddedCatalogCarriesOneKeySetForEveryLocale checks that the embedded catalog carries one key set for every locale.
func TestTheEmbeddedCatalogCarriesOneKeySetForEveryLocale(t *testing.T) {
	want := map[string]bool{}
	for _, key := range permissions.DescriptionKeys {
		want[key] = true
	}
	names, err := fs.Glob(localesFS, messageCatalog.Pattern)
	if err != nil {
		t.Fatalf("the catalog pattern %q is malformed: %v", messageCatalog.Pattern, err)
	}
	if len(names) < 2 {
		t.Fatalf("the catalog pattern %q matched %d file(s): a key set compared against one "+
			"language says nothing about the other", messageCatalog.Pattern, len(names))
	}
	for _, name := range names {
		raw, err := fs.ReadFile(localesFS, name)
		if err != nil {
			t.Fatalf("reading the embedded %s: %v", name, err)
		}
		var entries map[string]string
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("%s is not a JSON object of message text: %v", name, err)
		}
		for key := range want {
			if _, ok := entries[key]; !ok {
				t.Errorf("%s carries no entry for %q, the description of a permission this module "+
					"declares", name, key)
			}
		}
		for key := range entries {
			if !want[key] {
				t.Errorf("%s carries %q, which is no permission's description here", name, key)
			}
		}
		for key, text := range entries {
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s carries %q with no text behind it", name, key)
			}
		}
	}
}
