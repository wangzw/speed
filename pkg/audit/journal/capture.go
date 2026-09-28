package journal

import (
	"strconv"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
)

// captureCallbackName identifies this plugin's callbacks in GORM's own
// registry, which is per processor.
const captureCallbackName = "audit:capture"

// commitStep is the step GORM ends a statement's transaction at, and the anchor
// this plugin hangs its callbacks on.
//
// Anchoring on the statement's own step is not enough, and the difference is
// the whole of what this path promises. GORM registers that step without an
// anchor of its own, and its sort appends anything anchored *after* a step that
// is already ordered to the end of the chain — so a callback anchored after
// `gorm:create` lands after the commit, once the connection has been handed
// back to the pool. A row written there is written on a connection of its own
// again, after the fact: the record and the write stop being one unit of work,
// and a failure is reported against a statement that has already committed.
// Anchored before this step, the callback runs with the transaction still open,
// which is the only place the promise holds.
//
// Only the model-path processors are registered on. `Exec` and `Raw` are not,
// and leaving them out is what makes the self-reference judgement a comparison
// rather than a guard: a statement this plugin never sees cannot recurse
// through it. The price is stated as a non-guarantee — a host's own cleanup and
// a migration file issue raw SQL and reach no audit row.
const commitStep = "gorm:commit_or_rollback_transaction"

// capture is the plugin the descriptor declares.
//
// It holds nothing at all. The instance is frozen with the descriptor at init
// time, and the database module hands that one instance to every handle it
// builds, so a process driving two commands builds two registries that share
// it; anything kept on it would be the other run's value on the second one. The
// callbacks need nothing from it either — they take the session they are handed
// — which is why there is no per-handle state here to get wrong.
type capture struct{}

// newCapture builds the plugin the descriptor declares.
func newCapture() *capture { return &capture{} }

// Name identifies this plugin in GORM's registry of plugins, which is what the
// database module reports when two declarations collide. The convention there
// is the package path.
func (c *capture) Name() string { return "github.com/vislake/speed/pkg/audit/journal" }

// Initialize registers the capture callbacks on one handle.
//
// The handle is used to register and then dropped. GORM calls this once per
// handle, and what each callback works on is the session it is handed when it
// runs, so no handle has to be remembered and a second one needs no special
// care.
//
// The anchor is the step the transaction ends at, so the callback runs after
// the statement's own work and before the commit. The tenant filter registers
// on the same processors ahead of their own steps, so the two orders are
// decided by the anchors and not by which plugin was installed first.
func (c *capture) Initialize(handle *gorm.DB) error {
	registered := []func(*gorm.DB) error{
		func(h *gorm.DB) error {
			return h.Callback().Create().Before(commitStep).
				Register(captureCallbackName, captureOne("create"))
		},
		func(h *gorm.DB) error {
			return h.Callback().Update().Before(commitStep).
				Register(captureCallbackName, captureOne("update"))
		},
		func(h *gorm.DB) error {
			return h.Callback().Delete().Before(commitStep).
				Register(captureCallbackName, captureOne("delete"))
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
// The row is written on the session the statement ran on, which is what makes
// this path different from the three the module drives. The two consequences
// are the reason for it. The record stands or falls with the write it records —
// a transaction the caller opened and later rolled back takes the row with it,
// and no row is left saying a write happened that did not. And there is only
// ever one connection in play, so an engine that admits a single writer — the
// one this repository ships migrations for — has nothing to contend over.
//
// The price is that a failure here is raised rather than reported: the
// statement fails, because on this connection the record and the write are one
// unit of work. That is the one path in the module where that is true.
//
// A statement that has already failed is left alone. GORM runs every callback
// of a processor whatever the ones before it did, so this one is reached by a
// statement another plugin refused before it ran, by one GORM itself refused,
// and by one the engine turned down — and none of those changed anything, so
// there is no write to record. The row could not stand anyway: the transaction
// underneath is on its way to a rollback that takes it along, and an engine that
// refuses every further statement in a transaction one has failed in would not
// take it at all. Attempting it is worse than pointless, because the session
// the row goes out on carries the statement's error: the insert fails with that
// same error, and raising it would hand the caller its own failure dressed up as
// an audit store that could not be written to.
func captureOne(action string) func(*gorm.DB) {
	return func(tx *gorm.DB) {
		if tx.Error != nil {
			return
		}
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
			Outcome: statementOutcome,
			Detail: map[string]string{
				"rows_affected": strconv.FormatInt(tx.RowsAffected, 10),
			},
		}
		// A session of its own on the same connection: NewDB clears the
		// conditions the statement being recorded left on it, and the
		// transaction underneath — if there is one — is not one of them.
		session := tx.Session(&gorm.Session{NewDB: true, Context: st.Context})
		if err := insert(session, assemble(st.Context, audit.SourceStatement, entry, "")); err != nil {
			// The write that produced this row is not going to be reported any
			// other way: there is no caller to return to and no signal this
			// path reports through, because the row and the statement are one
			// unit. Raising it on the session is what fails the statement.
			_ = tx.AddError(err)
		}
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

// statementOutcome is what the result column holds on every statement row. A
// statement that failed leaves no row, so the one that does has succeeded; how
// much it changed is the detail's rows_affected.
const statementOutcome = "ok"
