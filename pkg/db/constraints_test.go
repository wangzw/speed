package db

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	gosqlite "github.com/glebarez/go-sqlite"
	"github.com/glebarez/sqlite"
	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
	sqlite3 "modernc.org/sqlite/lib"
)

func sqliteViolation(err error) error {
	var refusal *gosqlite.Error
	if !errors.As(err, &refusal) {
		return nil
	}
	switch refusal.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return gorm.ErrDuplicatedKey
	case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
		return gorm.ErrForeignKeyViolated
	default:
		return nil
	}
}

type uniqueRow struct {
	ID   uint
	Code string `gorm:"uniqueIndex"`
}

func constrainedHandle(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "constraints.db") + "?_pragma=foreign_keys(1)"
	handle, err := openHandle(t.Context(), sqliteSpec(), sqliteSection(dsn), discardingLogger())
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	t.Cleanup(func() {
		if err := Close(handle); err != nil {
			t.Errorf("releasing the handle: %v", err)
		}
	})
	if err := handle.AutoMigrate(&uniqueRow{}); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if err := handle.Create(&uniqueRow{ID: 1, Code: "taken"}).Error; err != nil {
		t.Fatalf("writing the row the refusals collide with: %v", err)
	}
	return handle
}

// TestAConstraintRefusalKeepsTheDriversError pins what the classification adds and what it leaves
// alone: the class is found by errors.Is.
func TestAConstraintRefusalKeepsTheDriversError(t *testing.T) {
	handle := constrainedHandle(t)

	err := handle.Create(&uniqueRow{ID: 2, Code: "taken"}).Error
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("a second row under a unique key gave %v, want gorm.ErrDuplicatedKey in its chain", err)
	}
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		t.Errorf("a duplicate reports a foreign key violation as well: %v", err)
	}
	var driver *gosqlite.Error
	if !errors.As(err, &driver) {
		t.Fatalf("the refusal no longer carries the driver's error: %#v", err)
	}
	if err.Error() != driver.Error() {
		t.Errorf("the refusal reads %q, want the driver's own text %q", err.Error(), driver.Error())
	}
}

// TestARefusalRaisedAgainIsClassifiedOnce covers a refusal that reaches a statement's error a
// second time — a plugin handing on the failure of a statement it ran on another session with %w.
func TestARefusalRaisedAgainIsClassifiedOnce(t *testing.T) {
	handle := constrainedHandle(t)
	refused := handle.Create(&uniqueRow{ID: 2, Code: "taken"}).Error
	raised := fmt.Errorf("a plugin's own write failed: %w", refused)

	for name, err := range map[string]error{"the refusal": refused, "the refusal raised again": raised} {
		var outer, inner *constraintViolation
		if !errors.As(classifyViolation(sqliteViolation, err), &outer) {
			t.Errorf("classifying %s again lost its classification", name)
			continue
		}
		if errors.As(outer.err, &inner) {
			t.Errorf("classifying %s again wrapped the classification in a second one", name)
		}
	}
}

// TestOnlyTheTwoClassesAreAttached pins that an implementation's answer is taken only when it is
// one of the two classes the contract names.
func TestOnlyTheTwoClassesAreAttached(t *testing.T) {
	driver := errors.New("the driver refused the statement")
	for _, class := range []error{gorm.ErrCheckConstraintViolated, gorm.ErrInvalidField, driver} {
		answer := func(error) error { return class }
		var attached *constraintViolation
		if got := classifyViolation(answer, driver); errors.As(got, &attached) {
			t.Errorf("an answer of %v was attached as %#v, want the driver's error as it is", class, got)
		}
	}
	for _, class := range []error{gorm.ErrDuplicatedKey, gorm.ErrForeignKeyViolated} {
		answer := func(error) error { return class }
		if got := classifyViolation(answer, driver); !errors.Is(got, class) || !errors.Is(got, driver) {
			t.Errorf("an answer of %v gave %#v, want both the class and the driver's error in the chain",
				class, got)
		}
	}
}

// TestTheDialectorKeepsEveryMethodOfTheEngines holds the wrapper every handle is opened with to the
// engines' own dialectors.
func TestTheDialectorKeepsEveryMethodOfTheEngines(t *testing.T) {
	wrapper := reflect.TypeFor[classifyingDialector]()
	for _, engine := range []gorm.Dialector{sqlite.Open("engine.db"), pgdriver.Open("host=engine")} {
		typ := reflect.TypeOf(engine)
		for i := range typ.NumMethod() {
			name := typ.Method(i).Name
			if _, carried := wrapper.MethodByName(name); !carried {
				t.Errorf("%s's dialector has %s, and the dialector every handle is opened with does "+
					"not carry it, so GORM would no longer reach it", engine.Name(), name)
			}
		}
	}
}

// TestTheEnginesAdjustmentsReachTheHandle observes one forwarded method at work: PostgreSQL's
// dialector sets the identifier length its engine allows when the handle is opened.
func TestTheEnginesAdjustmentsReachTheHandle(t *testing.T) {
	handle, err := gorm.Open(classifyingDialector{Dialector: pgdriver.Open("host=unreached"), classify: sqliteViolation},
		&gorm.Config{DisableAutomaticPing: true, TranslateError: true})
	if err != nil {
		t.Fatalf("opening a handle that never connects: %v", err)
	}
	t.Cleanup(func() { _ = Close(handle) })
	naming, ok := handle.NamingStrategy.(schema.NamingStrategy)
	if !ok || naming.IdentifierMaxLength != 63 {
		t.Errorf("the handle names identifiers with %#v, want PostgreSQL's limit of 63", handle.NamingStrategy)
	}
}
