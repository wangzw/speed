package journal

import (
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
)

// assertTheStatementsOwnFailure checks that err is the failure the statement
// itself ran into, and nothing this module added to it: the audit table in these
// cases is healthy, so a caller that tells an unavailable audit store from its
// own failure by ErrWriteFailed must not be told the store is at fault.
func assertTheStatementsOwnFailure(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("the statement gave %v, want its own failure %v", err, want)
	}
	if errors.Is(err, audit.ErrWriteFailed) {
		t.Fatalf("a statement that failed on its own was reported as an audit write failure: %v", err)
	}
	if strings.Contains(err.Error(), audit.ErrWriteFailed.Error()) {
		t.Fatalf("the statement's error carries the audit write failure's text: %v", err)
	}
}

// A statement that fails on its own keeps its own error and leaves no row.
//
// A duplicate key is the ordinary case: the table is healthy, the statement is
// not, and a caller mapping ErrWriteFailed to "audit unavailable" would answer a
// conflict as an outage if the record of the failed statement were attempted on
// a session that carries the statement's error.
func TestAStatementThatFailsKeepsItsOwnError(t *testing.T) {
	handle := open(t)
	if err := handle.Create(&order{ID: "o1", Note: "first"}).Error; err != nil {
		t.Fatalf("writing the order: %v", err)
	}

	err := handle.Create(&order{ID: "o1", Note: "again"}).Error
	if err == nil {
		t.Fatal("a duplicate key was accepted")
	}
	if errors.Is(err, audit.ErrWriteFailed) || strings.Contains(err.Error(), audit.ErrWriteFailed.Error()) {
		t.Fatalf("a duplicate key was reported as an audit write failure: %v", err)
	}
	if got := len(auditRows(t, handle)); got != 1 {
		t.Fatalf("want the one successful write recorded and the failed one not, got %d row(s)", got)
	}
}

// The same holds on the update and delete processors, which register the same
// callback: a statement GORM refuses before it runs keeps GORM's refusal.
func TestARefusedUpdateOrDeleteKeepsItsOwnError(t *testing.T) {
	handle := open(t)

	err := handle.Model(&order{}).Update("note", "everywhere").Error
	assertTheStatementsOwnFailure(t, err, gorm.ErrMissingWhereClause)

	err = handle.Delete(&order{}).Error
	assertTheStatementsOwnFailure(t, err, gorm.ErrMissingWhereClause)

	if got := len(auditRows(t, handle)); got != 0 {
		t.Fatalf("a refused statement left %d audit row(s)", got)
	}
}

// errRefusedByAnotherPlugin stands for a refusal another plugin raises on the
// statement before it runs — the tenant filter refusing a scoped statement with
// no tenant in its context is the one this repository ships.
var errRefusedByAnotherPlugin = errors.New("refused by another plugin")

// A refusal another plugin raised ahead of the statement reaches the caller as
// that refusal, and the statement it refused leaves no row.
func TestAStatementAnotherPluginRefusedKeepsThatRefusal(t *testing.T) {
	handle := open(t)
	if err := handle.Callback().Create().Before("gorm:create").
		Register("test:refuse", func(tx *gorm.DB) { _ = tx.AddError(errRefusedByAnotherPlugin) }); err != nil {
		t.Fatalf("registering the refusal: %v", err)
	}

	err := handle.Create(&order{ID: "o1", Note: "refused"}).Error
	assertTheStatementsOwnFailure(t, err, errRefusedByAnotherPlugin)

	if got := len(auditRows(t, handle)); got != 0 {
		t.Fatalf("a refused statement left %d audit row(s)", got)
	}
}
