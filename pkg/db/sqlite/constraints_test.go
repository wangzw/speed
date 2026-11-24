package sqlite_test

import (
	"errors"
	"testing"

	gosqlite "github.com/glebarez/go-sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/db/internal/dbsuite"
)

func carriesSQLiteError(err error) bool {
	var refusal *gosqlite.Error
	return errors.As(err, &refusal)
}

// TestConstraintRefusalsOnTheDeliveredHandle runs the engine-neutral cases on the handle this
// engine's module delivers.
func TestConstraintRefusalsOnTheDeliveredHandle(t *testing.T) {
	capability := deliver(t, enforcingLocator(t, "delivered.db"))
	dbsuite.ConstraintRefusals(t, capability.DB(), carriesSQLiteError)
}

// TestConstraintRefusalsOnAnOpenedHandle runs them on a connection built with Open, which is
// assembled the way the delivered one is.
func TestConstraintRefusalsOnAnOpenedHandle(t *testing.T) {
	capability := deliver(t, enforcingLocator(t, "delivered.db"))
	opened, err := capability.Open(t.Context(), enforcingLocator(t, "opened.db"))
	if err != nil {
		t.Fatalf("opening a second connection: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(opened) })
	dbsuite.ConstraintRefusals(t, opened, carriesSQLiteError)
}

// TestADuplicateRowidIsADuplicatedKey covers the uniqueness refusal of a table without an INTEGER
// PRIMARY KEY written with an explicit rowid.
func TestADuplicateRowidIsADuplicatedKey(t *testing.T) {
	handle := deliver(t, enforcingLocator(t, "rowid.db")).DB()
	if err := handle.Exec("CREATE TABLE notes (body TEXT)").Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if err := handle.Exec("INSERT INTO notes (rowid, body) VALUES (1, 'first')").Error; err != nil {
		t.Fatalf("writing the first row: %v", err)
	}
	err := handle.Exec("INSERT INTO notes (rowid, body) VALUES (1, 'second')").Error
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Errorf("a second row under one rowid reported %v, want gorm.ErrDuplicatedKey in its chain", err)
	}
}
