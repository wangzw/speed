package preferences

import (
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/tenant"
)

// TestTheModuleIsNamedAsDesigned checks that Module and ModuleName report
// "notification.preferences".
func TestTheModuleIsNamedAsDesigned(t *testing.T) {
	if Module().Name != "notification.preferences" {
		t.Errorf("the module is named %q, want notification.preferences", Module().Name)
	}
	if ModuleName != "notification.preferences" {
		t.Errorf("ModuleName is %q, want notification.preferences", ModuleName)
	}
}

// TestTheDescriptorDeliversPreferencesExclusively holds that the preference seam
// is delivered exclusively, so one process has exactly one preference matrix.
func TestTheDescriptorDeliversPreferencesExclusively(t *testing.T) {
	found := false
	for _, p := range Module().Provides {
		if p.Token == core.Token((*notification.Preferences)(nil)) {
			found = p.Exclusive
		}
	}
	if !found {
		t.Error("the descriptor does not deliver (*notification.Preferences)(nil) exclusively")
	}
}

// TestTheDescriptorRequiresItsDependencies checks which capabilities are
// required and which are optional.
func TestTheDescriptorRequiresItsDependencies(t *testing.T) {
	required := map[core.Token]bool{}
	optional := map[core.Token]bool{}
	for _, r := range Module().Requires {
		if r.Optional {
			optional[r.Token] = true
		} else {
			required[r.Token] = true
		}
	}
	for _, token := range []core.Token{(*db.Database)(nil), (*tenant.Catalog)(nil)} {
		if !required[token] {
			t.Errorf("%T is not a required dependency", token)
		}
	}
	if !optional[core.Token((*speedhttp.Router)(nil))] {
		t.Error("the HTTP entry point is not optional")
	}
	if !optional[core.Token((*log.Logger)(nil))] {
		t.Error("the logging capability is not optional")
	}
}

// TestTheDescriptorDeclaresItsConfigNamespace holds that this module
// declares a configuration schema of its own under its own name.
func TestTheDescriptorDeclaresItsConfigNamespace(t *testing.T) {
	var declared config.Schema
	found := false
	for _, res := range Module().Resources {
		if s, isSchema := res.(config.Schema); isSchema {
			declared, found = s, true
		}
	}
	if !found {
		t.Fatal("the descriptor carries no config schema resource")
	}
	if declared.Namespace != ConfigNamespace {
		t.Errorf("the schema is mounted under %q, want %q", declared.Namespace, ConfigNamespace)
	}
	if _, ok := declared.Items[endpointsItem]; !ok {
		t.Errorf("the schema declares no %q item", endpointsItem)
	}
}

// TestTheModuleDeclaresItsMigrationSet holds that the descriptor carries a
// migration resource with a file for each dialect.
func TestTheModuleDeclaresItsMigrationSet(t *testing.T) {
	var migrations db.Migrations
	found := false
	for _, res := range Module().Resources {
		if m, ok := res.(db.Migrations); ok {
			migrations, found = m, true
		}
	}
	if !found || migrations.FS == nil {
		t.Fatal("the module declares no db.Migrations resource")
	}
	for _, dialect := range []string{"postgres", "sqlite"} {
		entries, err := fs.ReadDir(migrations.FS, dialect)
		if err != nil {
			t.Fatalf("listing the %s migrations: %v", dialect, err)
		}
		if len(entries) == 0 {
			t.Errorf("the %s migration directory is empty", dialect)
		}
	}
}

// TestTheModuleDeclaresItsMessageCatalog holds that the catalog is declared to
// the i18n engine and that the two locale files carry the same key set.
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
	for _, code := range []string{badRequestCode, notUnsubscribableCode} {
		for _, suffix := range []string{titleSuffix, detailSuffix} {
			key := "notification.preferences." + code + suffix
			if _, ok := en[key]; !ok {
				t.Errorf("the English catalog carries no %q", key)
			}
			if _, ok := zh[key]; !ok {
				t.Errorf("the Chinese catalog carries no %q", key)
			}
		}
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
