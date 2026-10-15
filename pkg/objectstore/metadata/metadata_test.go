package metadata

import (
	"encoding/json"
	"io/fs"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authz"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/i18n"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/tenant"
)

// TestTheModuleIsNamedAsDesigned pins the module name and its configuration
// namespace, which are the same string.
func TestTheModuleIsNamedAsDesigned(t *testing.T) {
	if Module().Name != "objectstore.metadata" {
		t.Errorf("the module is named %q, want objectstore.metadata", Module().Name)
	}
	if ConfigNamespace != "objectstore.metadata" {
		t.Errorf("the configuration namespace is %q, want objectstore.metadata", ConfigNamespace)
	}
}

// TestTheDescriptorDeliversObjectsExclusively holds that the metadata seam is
// delivered exclusively, so one process has exactly one metadata surface.
func TestTheDescriptorDeliversObjectsExclusively(t *testing.T) {
	found := false
	for _, p := range Module().Provides {
		if p.Token == core.Token((*objectstore.Objects)(nil)) {
			found = p.Exclusive
		}
	}
	if !found {
		t.Error("the descriptor does not deliver (*objectstore.Objects)(nil) exclusively")
	}
}

// TestTheDescriptorRequiresItsDependencies holds which capabilities are required
// and which are optional.
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
	for _, token := range []core.Token{
		(*db.Database)(nil), (*tenant.Catalog)(nil), (*objectstore.Store)(nil),
	} {
		if !required[token] {
			t.Errorf("%T is not a required dependency", token)
		}
	}
	if !optional[core.Token((*log.Logger)(nil))] {
		t.Error("the logging capability is not optional")
	}
	if !optional[core.Token((*speedhttp.Router)(nil))] {
		t.Error("the HTTP entry point is not optional")
	}
}

// TestTheDescriptorPointsNoDependencyAtTheEventOrAuditCapability holds that the
// module publishes no event and writes no audit row: it declares the audit action
// type names nowhere and depends on neither capability.
func TestTheDescriptorPointsNoDependencyAtTheEventOrAuditCapability(t *testing.T) {
	for _, r := range Module().Requires {
		switch r.Token {
		case core.Token((*event.Bus)(nil)):
			t.Error("the module requires the event bus, but it publishes nothing")
		case core.Token((*audit.Audit)(nil)):
			t.Error("the module requires the audit capability, but it writes no audit row")
		}
	}
}

// TestTheAuditActionNamesAreDeclaredByTheRootPackage holds that the three audit
// action type names are declared where the host reads them.
func TestTheAuditActionNamesAreDeclaredByTheRootPackage(t *testing.T) {
	want := map[string]string{
		objectstore.EventObjectCreated:   "objectstore.object.created",
		objectstore.EventObjectCompleted: "objectstore.object.completed",
		objectstore.EventObjectDeleted:   "objectstore.object.deleted",
	}
	for got, wantName := range want {
		if got != wantName {
			t.Errorf("an audit action name is %q, want %q", got, wantName)
		}
	}
}

// TestTheModuleDeclaresItsPermissionVocabulary holds that the two permissions
// reach the process catalog and that each has a translation row.
func TestTheModuleDeclaresItsPermissionVocabulary(t *testing.T) {
	var spec authz.PermissionSpec
	var translation authz.TranslationSpec
	var seenSpec, seenTranslation bool
	for _, res := range Module().Resources {
		switch v := res.(type) {
		case authz.PermissionSpec:
			spec, seenSpec = v, true
		case authz.TranslationSpec:
			translation, seenTranslation = v, true
		}
	}
	if !seenSpec || !seenTranslation {
		t.Fatalf("resources carry spec=%v translation=%v, want both", seenSpec, seenTranslation)
	}
	declared := map[authz.Permission]bool{}
	for _, p := range spec.Permissions {
		declared[p] = true
	}
	for _, p := range []authz.Permission{PermissionObjectRead, PermissionObjectWrite} {
		if !declared[p] {
			t.Errorf("the permission %q is not declared", p)
		}
		if _, ok := translation.Rows[p]; !ok {
			t.Errorf("the permission %q has no translation row", p)
		}
	}
	if row := translation.Rows[PermissionObjectRead]; row.Object != objectResource || row.Action != "read" {
		t.Errorf("object.read translates to %+v, want %q/read", row, objectResource)
	}
	if row := translation.Rows[PermissionObjectWrite]; row.Object != objectResource || row.Action != "write" {
		t.Errorf("object.write translates to %+v, want %q/write", row, objectResource)
	}
}

// TestTheModuleDeclaresItsMessageCatalog holds that the catalog is declared to the
// i18n engine and that the two locale files carry the same key set.
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
}

// localeKeys reads one locale file's key set.
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
