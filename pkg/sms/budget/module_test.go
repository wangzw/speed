package budget

import (
	"io/fs"
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/settings"
)

// TestTheModuleShipsAMigrationForEveryDialect holds the descriptor's migration
// resource to the two engines the database module runs on: a dialect with no
// subdirectory is zero migrations, which fails silently, so the files are
// checked here rather than discovered at a deployment.
func TestTheModuleShipsAMigrationForEveryDialect(t *testing.T) {
	for _, dialect := range []string{string(db.Postgres), string(db.SQLite)} {
		entries, err := fs.ReadDir(migrationsFS, dialect)
		if err != nil {
			t.Fatalf("listing the %s migrations: %v", dialect, err)
		}
		if len(entries) == 0 {
			t.Errorf("the module ships no %s migration", dialect)
		}
	}
}

// TestTheModuleDeliversTheStoreAndItsMigrations holds the descriptor's shape: a
// consumer's requirement on Store has to find one provider, and the database
// module has to find the table's migration set on the module's resources.
func TestTheModuleDeliversTheStoreAndItsMigrations(t *testing.T) {
	m := Module()
	if m.Name != ModuleName {
		t.Errorf("the module name = %q, want %q", m.Name, ModuleName)
	}
	if len(m.Provides) != 1 {
		t.Fatalf("the module declares %d provisions, want 1", len(m.Provides))
	}
	if _, ok := m.Provides[0].Token.(*Store); !ok {
		t.Errorf("the module provides %T, want the Store capability", m.Provides[0].Token)
	}
	if m.Provides[0].Exclusive {
		t.Error("the module claims Store exclusively; one process has one ledger and every consumer reads it")
	}
	found := false
	for _, resource := range m.Resources {
		if _, ok := resource.(db.Migrations); ok {
			found = true
		}
	}
	if !found {
		t.Error("the module declares no db.Migrations resource, so its table is never created")
	}
}

// TestTheModuleDeclaresTheCrossChannelCeiling holds the one setting this module
// owns to its declaration: the shared sms namespace, the key both sending
// channels read, the deployment default of 5000, and a domain that admits a
// count of sends in an hour and nothing else.
func TestTheModuleDeclaresTheCrossChannelCeiling(t *testing.T) {
	var declared settings.Schema
	found := false
	for _, resource := range Module().Resources {
		if schema, ok := resource.(settings.Schema); ok {
			declared, found = schema, true
		}
	}
	if !found {
		t.Fatal("the module declares no settings.Schema, so the cross-channel ceiling is not a setting")
	}
	if ceilingNamespace != "sms" {
		t.Errorf("the shared namespace constant = %q, want sms", ceilingNamespace)
	}
	if declared.Namespace != ceilingNamespace {
		t.Errorf("the settings namespace is %q, want %q", declared.Namespace, ceilingNamespace)
	}
	item, ok := declared.Items[ceilingHourlyItem]
	if !ok {
		t.Fatalf("the settings schema declares %v, want the %q item", declared.Items, ceilingHourlyItem)
	}
	if item.Description == "" {
		t.Error("the ceiling item declares no description")
	}
	if item.Domain.Rule() == "" {
		t.Error("the ceiling item declares no domain")
	}
	if len(declared.Mounts) != 1 {
		t.Fatalf("the ceiling schema mounts %d prototypes, want 1", len(declared.Mounts))
	}
	prototype, ok := declared.Mounts[0].Value.(*ceilingDefaults)
	if !ok {
		t.Fatalf("the ceiling schema mounts %T, want a *ceilingDefaults", declared.Mounts[0].Value)
	}
	if prototype.CeilingHourly != 5000 {
		t.Errorf("ceiling-hourly default = %d, want 5000", prototype.CeilingHourly)
	}
	for _, n := range []int{1, 1000000} {
		if !ceilingHourlyInDomain(n) {
			t.Errorf("the ceiling domain refuses its bound %d", n)
		}
	}
	for _, n := range []int{0, 1000001} {
		if ceilingHourlyInDomain(n) {
			t.Errorf("the ceiling domain admits %d, which is outside it", n)
		}
	}
}

// TestTheExportedCeilingConstantsAreTheDeclaredOnes holds the one statement both
// sending channels read: the deployment default and the domain bounds are
// exported so a consumer's fallback and domain check cannot drift from the
// declaration.
func TestTheExportedCeilingConstantsAreTheDeclaredOnes(t *testing.T) {
	if DefaultCeilingHourly != 5000 {
		t.Errorf("DefaultCeilingHourly = %d, want 5000", DefaultCeilingHourly)
	}
	if MinCeilingHourly != 1 {
		t.Errorf("MinCeilingHourly = %d, want 1", MinCeilingHourly)
	}
	if MaxCeilingHourly != 1000000 {
		t.Errorf("MaxCeilingHourly = %d, want 1000000", MaxCeilingHourly)
	}
	if ceilingDefaultValues.CeilingHourly != DefaultCeilingHourly {
		t.Errorf("the declared default is %d, want the exported constant %d",
			ceilingDefaultValues.CeilingHourly, DefaultCeilingHourly)
	}
	for _, n := range []int{MinCeilingHourly, MaxCeilingHourly} {
		if !ceilingHourlyInDomain(n) {
			t.Errorf("the ceiling domain refuses its bound %d", n)
		}
	}
}
