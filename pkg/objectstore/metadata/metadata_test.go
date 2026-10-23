package metadata

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/config"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/log"
	"github.com/vislake/speed/pkg/objectstore"
	"github.com/vislake/speed/pkg/settings"
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
	for _, token := range []core.Token{
		(*objectstore.DirectURL)(nil), (*settings.Store)(nil), (*log.Logger)(nil),
	} {
		if !optional[token] {
			t.Errorf("%T is not an optional dependency", token)
		}
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

// TestTheModuleDeclaresOnlyItsOwnResources holds that the descriptor carries the
// configuration schema, the settings schema and the migrations and nothing else:
// the permission, feature, translation, message-catalog and OpenAPI resources an
// earlier design registered are gone with the API surface that needed them.
func TestTheModuleDeclaresOnlyItsOwnResources(t *testing.T) {
	var (
		schemaCount    int
		settingsCount  int
		migrationCount int
	)
	for _, res := range Module().Resources {
		switch res.(type) {
		case config.Schema:
			schemaCount++
		case settings.Schema:
			settingsCount++
		case db.Migrations:
			migrationCount++
		default:
			t.Errorf("the descriptor declares an unexpected resource %T", res)
		}
	}
	if schemaCount != 1 {
		t.Errorf("the descriptor declares %d configuration schemas, want one", schemaCount)
	}
	if settingsCount != 1 {
		t.Errorf("the descriptor declares %d settings schemas, want one", settingsCount)
	}
	if migrationCount != 1 {
		t.Errorf("the descriptor declares %d migration sets, want one", migrationCount)
	}
}

// TestTheGrantsTableIsCreatedOnEveryEngine drives the migrations and reads the
// table the content route depends on, so a dialect the SQL was not written for
// fails here rather than at the first minted URL.
func TestTheGrantsTableIsCreatedOnEveryEngine(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(_ context.Context, reg *core.Registry) error {
			var n int64
			if err := dbHandle(t, reg).Raw("SELECT count(*) FROM object_grants").Row().Scan(&n); err != nil {
				t.Fatalf("reading the object_grants table: %v", err)
			}
			if n != 0 {
				t.Errorf("a fresh object_grants table holds %d rows, want none", n)
			}
			return nil
		})
	})
}
