//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/postgres"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// runAssembly runs a real host around the module this subpackage registers, and
// returns the capability the dependant resolved.
//
// The configuration is written under this implementation's namespace. An
// implementation that mounted its items somewhere else is therefore not
// configured at all: it disables itself, and what the case reports is the
// dependant's unmet requirement rather than a run that quietly did nothing. The
// server behind the locator comes from the compose stack, so only an integration
// run reaches this.
func runAssembly(t *testing.T, dsn string) (db.Database, error) {
	t.Helper()
	return runAssemblyWith(t, context.Background(), map[string]any{"dsn": dsn})
}

// runAssemblyWith is runAssembly over a section the case writes in full, driven
// under a context the case hands in: a deadline on it is a deadline on the
// whole startup, the way a host bounds one.
func runAssemblyWith(t *testing.T, parent context.Context, section map[string]any) (db.Database, error) {
	t.Helper()
	return runAssemblySupplying(t, parent, section, nil)
}

// TestTheRegisteredModuleAppliesThisEnginesMigrations runs the module this
// subpackage registers, end to end, on a database of its own.
//
// The registration is the deliverable here, and this is what says it is worth
// anything: a host that imports the package and configures the namespace this
// implementation owns gets a capability, resolutions succeed against it, and
// the migration sets declared for this engine are applied on it. What it pins
// beyond the shape case is the wiring — the dialect that names the migration
// subdirectory, the driver binding that opens that locator, and the migrate
// stage that reaches both. The index in the record below is the engine's third
// admission condition seen from here: the file holds two statements and the run
// hands it over whole, without splitting the text.
func TestTheRegisteredModuleAppliesThisEnginesMigrations(t *testing.T) {
	dsn := pgtest.Acquire(t)

	capability, err := runAssembly(t, dsn)
	if err != nil {
		t.Fatalf("a host that imported this implementation and configured its namespace did not start: %v", err)
	}
	if got := capability.Dialect(); got != postgres.Dialect {
		t.Errorf("the delivered capability reports the %q engine, and this subpackage binds %q",
			got, postgres.Dialect)
	}

	direct := open(t, dsn)
	if !tableExists(t, direct, "widgets") {
		t.Error("the table the dependant's migration creates is missing, so the run did not read this " +
			"engine's migration subdirectory")
	}
	// to_regclass resolves any relation, an index included. The index comes
	// from the second statement of one file, so its absence is what an engine
	// that stopped at the first statement looks like.
	if !tableExists(t, direct, "widgets_name_idx") {
		t.Error("the index the second statement of the migration creates is missing, so the run did " +
			"not carry the whole file to the driver")
	}

	type applied struct {
		Module string
		File   string
	}
	var records []applied
	if err := direct.Raw("SELECT module, file FROM db_migrations ORDER BY module, file").Scan(&records).Error; err != nil {
		t.Fatalf("reading the migration record table: %v", err)
	}
	if len(records) != 1 || records[0].Module != dependantName || records[0].File != dependantFile {
		t.Errorf("the record table holds %v, and this run should have recorded one migration, %s/%s: "+
			"the table being there without its record means the table above came from somewhere else",
			records, dependantName, dependantFile)
	}
}
