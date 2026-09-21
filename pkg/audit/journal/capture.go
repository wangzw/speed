package journal

import (
	"strconv"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
)

// captureCallbackName identifies this plugin's callbacks in GORM's own
// registry, which is per processor.
const captureCallbackName = "audit:capture"

// The processors this plugin registers on, and the steps its anchors name.
//
// Only the model-path processors are here. `Exec` and `Raw` are not, and
// leaving them out is what makes the self-reference judgement a comparison
// rather than a guard: a statement this plugin never sees cannot recurse
// through it. The price is stated as a non-guarantee — a host's own cleanup and
// a migration file issue raw SQL and reach no audit row.
const (
	createProcessor = "gorm:create"
	updateProcessor = "gorm:update"
	deleteProcessor = "gorm:delete"
)

// capture is the plugin the descriptor declares.
//
// It holds nothing. The instance is frozen with the descriptor at init time,
// and the database module hands that one instance to every handle it builds, so
// a process driving two commands builds two registries that share it and
// anything kept on it would be the other run's value on the second one. What a
// callback needs is closed over as the callback is registered instead: the
// handle that Initialize was given, and a failure signal belonging to that
// handle alone.
type capture struct{}

// newCapture builds the plugin the descriptor declares.
func newCapture() *capture { return &capture{} }

// Name identifies this plugin in GORM's registry of plugins, which is what the
// database module reports when two declarations collide. The convention there
// is the package path.
func (c *capture) Name() string { return "github.com/vislake/speed/pkg/audit/journal" }

// Initialize registers the capture callbacks on one handle.
//
// The handle is captured, not kept on the instance, and GORM calls this once
// per handle. The anchor names a step rather than a position: what it gives is
// a lower bound, and the sort puts the callback at the end of the processor's
// chain — after the statement has run and after the transaction it was in has
// been committed or rolled back. The tenant filter registers on the same
// processors ahead of their own steps, so the two orders are decided by the
// anchors and not by which plugin was installed first.
func (c *capture) Initialize(handle *gorm.DB) error {
	sig := newSignal()
	registered := []func(*gorm.DB) error{
		func(h *gorm.DB) error {
			return h.Callback().Create().After(createProcessor).
				Register(captureCallbackName, captureOne(h, sig, "create"))
		},
		func(h *gorm.DB) error {
			return h.Callback().Update().After(updateProcessor).
				Register(captureCallbackName, captureOne(h, sig, "update"))
		},
		func(h *gorm.DB) error {
			return h.Callback().Delete().After(deleteProcessor).
				Register(captureCallbackName, captureOne(h, sig, "delete"))
		},
	}
	for _, register := range registered {
		if err := register(handle); err != nil {
			return err
		}
	}
	return nil
}

// captureOne builds the callback for one processor.
//
// handle is the root handle, so the row is written on a connection of its own
// and not through the session the business statement ran in. A failure to write
// therefore neither rolls the statement back nor is rolled back by it, and it
// is reported rather than raised.
func captureOne(handle *gorm.DB, sig *signal, action string) func(*gorm.DB) {
	return func(tx *gorm.DB) {
		st := tx.Statement
		if st == nil {
			return
		}
		table := targetTable(st)
		if table == "" || table == tableName {
			return
		}
		entry := audit.Entry{
			Action:  action,
			Object:  table,
			Outcome: outcomeOf(tx),
			Detail: map[string]string{
				"rows_affected": strconv.FormatInt(tx.RowsAffected, 10),
			},
		}
		if err := insert(handle.WithContext(st.Context), assemble(st.Context, audit.SourceStatement, entry, "")); err != nil {
			sig.fail(err)
			return
		}
		sig.recover()
	}
}

// targetTable is the table a statement writes to.
//
// Statement.Table is what the model path sets; the schema's own name is the
// fallback for a statement that reached this point without one. The comparison
// against tableName is what keeps this plugin from recording its own writes,
// and it holds because the write goes through the same model path — a write
// issued as raw SQL would carry no table and would not reach here at all.
func targetTable(st *gorm.Statement) string {
	if st.Table != "" {
		return st.Table
	}
	if st.Schema != nil {
		return st.Schema.Table
	}
	return ""
}

// outcomeOf renders how a statement ended.
func outcomeOf(tx *gorm.DB) string {
	if tx.Error != nil {
		return "failed"
	}
	return "ok"
}
