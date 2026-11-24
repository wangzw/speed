package inbox

import (
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/vislake/speed/pkg/i18n"
)

// unavailableTitleKey is the key the module composes for the 503's title:
// the refusal that carries no problem type writes that one and nothing
// else.
const unavailableTitleKey = ModuleName + ".unavailable" + titleSuffix

// TestTheModuleDeclaresItsMessageCatalog checks that the catalog's two locale
// files carry the same keys and every key composed at render time.
func TestTheModuleDeclaresItsMessageCatalog(t *testing.T) {
	var catalog i18n.Catalog
	found := false
	for _, res := range Module().Resources {
		if c, ok := res.(i18n.Catalog); ok {
			catalog, found = c, true
		}
	}
	if !found {
		t.Fatal("the module declares no i18n.Catalog resource")
	}
	if catalog.FS == nil || catalog.Pattern != "locales/*.json" {
		t.Fatalf("the catalog is %+v, want a locales/*.json pattern over an embedded filesystem", catalog)
	}
	en := localeKeys(t, "locales/en-US.json")
	zh := localeKeys(t, "locales/zh-CN.json")
	if len(en) == 0 {
		t.Fatal("the English catalog is empty")
	}
	for key := range en {
		if _, ok := zh[key]; !ok {
			t.Errorf("the key %q is in the English catalog but not the Chinese one", key)
		}
	}
	for key := range zh {
		if _, ok := en[key]; !ok {
			t.Errorf("the key %q is in the Chinese catalog but not the English one", key)
		}
	}
	// The two error codes this surface writes must each carry a title and a
	// detail in both catalogs.
	for _, code := range []string{badRequestCode, messageNotFoundCode} {
		for _, suffix := range []string{titleSuffix, detailSuffix} {
			key := ModuleName + "." + code + suffix
			if _, ok := en[key]; !ok {
				t.Errorf("the English catalog carries no %q", key)
			}
			if _, ok := zh[key]; !ok {
				t.Errorf("the Chinese catalog carries no %q", key)
			}
		}
	}
	if _, ok := en[unavailableTitleKey]; !ok {
		t.Errorf("the English catalog carries no %q", unavailableTitleKey)
	}
	if _, ok := zh[unavailableTitleKey]; !ok {
		t.Errorf("the Chinese catalog carries no %q", unavailableTitleKey)
	}
}

func localeKeys(t *testing.T, name string) map[string]string {
	t.Helper()
	raw, err := fs.ReadFile(localesFS, name)
	if err != nil {
		t.Fatalf("reading %q: %v", name, err)
	}
	entries := map[string]string{}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decoding %q: %v", name, err)
	}
	return entries
}
