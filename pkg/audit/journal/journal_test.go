package journal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/event"
	speedhttp "github.com/vislake/speed/pkg/http"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/log"
)

// order is the model the capture tests write through. It is an ordinary model:
// nothing here is scoped, so the tenant filter has no part in these cases and
// the only plugin on the handle is the one under test.
type order struct {
	ID   string `gorm:"column:id;primaryKey"`
	Note string `gorm:"column:note"`
}

func (order) TableName() string { return "orders" }

const createOrders = `CREATE TABLE orders (
	id   TEXT PRIMARY KEY,
	note TEXT NOT NULL
)`

// auditDialect is the dialect these cases run on, and the subdirectory of the
// migrations the module ships for it. The table the cases write into is built
// from those files rather than from a copy written out here: a copy is a second
// description of the schema, and the two part company at the first column
// someone adds.
const auditDialect = "sqlite"

// applyMigration runs one of the module's own migration files.
//
// The statements go through Exec, which does not reach the plugin — reaching it
// needs the model path. That is also how the production run applies them.
func applyMigration(t *testing.T, handle *gorm.DB, name string) {
	t.Helper()
	sql, err := fs.ReadFile(migrationsFS, name)
	if err != nil {
		t.Fatalf("reading the migration %s: %v", name, err)
	}
	for _, statement := range strings.Split(string(sql), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("applying %s: %v", name, err)
		}
	}
}

// applyMigrations runs every migration file the module ships for one dialect,
// in the order the database module applies them: by file name.
func applyMigrations(t *testing.T, handle *gorm.DB, dialect string) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, dialect)
	if err != nil {
		t.Fatalf("listing the %s migrations: %v", dialect, err)
	}
	for _, entry := range entries {
		applyMigration(t, handle, dialect+"/"+entry.Name())
	}
}

// newTestJournal builds a product that is accepting, reports nowhere and logs
// nowhere: what these cases assert is what reached the table.
func newTestJournal(handle *gorm.DB, set settings) *journal {
	j := newJournal(set, handle, slog.New(slog.NewTextHandler(io.Discard, nil)))
	j.signal = &signal{out: io.Discard}
	j.accepting.Store(true)
	return j
}

// recordingBus is a bus that keeps what was subscribed to it, so a case can
// hand the module a fact the way a bus would.
type recordingBus struct {
	mu       sync.Mutex
	handlers map[string][]event.Handler
}

func newRecordingBus() *recordingBus {
	return &recordingBus{handlers: map[string][]event.Handler{}}
}

func (b *recordingBus) Subscribe(eventType string, h event.Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], h)
}

// Publish plays the publisher's part: every handler the type has registered is
// called, which is what a process-local bus does.
func (b *recordingBus) Publish(ctx context.Context, evt event.Event) error {
	b.mu.Lock()
	handlers := append([]event.Handler(nil), b.handlers[evt.Type]...)
	b.mu.Unlock()
	for _, h := range handlers {
		_ = h(ctx, evt)
	}
	return nil
}

// sharedMemory is a handle on a database of this call's own, which every
// connection of the handle's pool shares: the one the database module delivers
// over a SQLite file, released when the case ends.
//
// It is the database module's handle rather than one opened here, so that the
// journal runs over what an assembly hands it: the constraint classification
// the module installs, and the pool shape it gives the locator. The session
// handed back writes no record of a statement a case fails on purpose.
func sharedMemory(t *testing.T) *gorm.DB {
	t.Helper()
	return quietly(dbtest.SQLite(t, filepath.Join(t.TempDir(), "journal.db"), nil).DB())
}

// open builds a handle with the capture plugin installed and both tables
// created. The tables are created with raw SQL, which the plugin does not
// capture — reaching the plugin needs the model path.
func open(t *testing.T) *gorm.DB {
	t.Helper()
	handle := sharedMemory(t)
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	if err := handle.Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	applyMigrations(t, handle, auditDialect)
	return handle
}

// auditRows reads back what landed in the audit table.
func auditRows(t *testing.T, handle *gorm.DB) []row {
	t.Helper()
	var rows []row
	if err := handle.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("reading the audit table: %v", err)
	}
	return rows
}

// A model write is recorded once, with the path, verb and object the design
// names for it.
func TestStatementCaptureRecordsAModelWrite(t *testing.T) {
	handle := open(t)
	if err := handle.Create(&order{ID: "o1", Note: "first"}).Error; err != nil {
		t.Fatalf("writing the order: %v", err)
	}
	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want exactly one audit row, got %d", len(rows))
	}
	got := rows[0]
	if got.Source != string(audit.SourceStatement) {
		t.Errorf("source is %q, want %q", got.Source, audit.SourceStatement)
	}
	if got.Action != "create" {
		t.Errorf("action is %q, want %q", got.Action, "create")
	}
	if got.Object != "orders" {
		t.Errorf("object is %q, want %q", got.Object, "orders")
	}
	if got.Outcome != "ok" {
		t.Errorf("outcome is %q, want %q", got.Outcome, "ok")
	}
}

// The module's own writes are left alone.
//
// This is the case the self-reference judgement exists for, and it is
// discriminating in the direction that matters: without the judgement the write
// below records itself, which records itself, and the test dies of a stack
// overflow rather than of an assertion.
func TestStatementCaptureLeavesItsOwnWritesAlone(t *testing.T) {
	handle := open(t)
	if err := handle.Create(&row{
		OccurredAt: time.Now().UTC(),
		Source:     string(audit.SourceSeam),
		Action:     "seam",
		Object:     tableName,
		Outcome:    "ok",
		Detail:     "{}",
	}).Error; err != nil {
		t.Fatalf("writing an audit row: %v", err)
	}
	// Exactly the row written above, and no second one about it. Without the
	// judgement this write records itself, which records itself, and the test
	// dies of a stack overflow rather than of this assertion.
	if got := len(auditRows(t, handle)); got != 1 {
		t.Fatalf("want the one row written above and no record of it, got %d row(s)", got)
	}
}

// A statement-level row rides the statement's transaction.
//
// This is the decision the path turns on: the record and the write it describes
// are one unit of work. Writing on a connection of its own instead leaves the
// row behind after the rollback, and this case says so.
func TestStatementCaptureRidesTheStatementsTransaction(t *testing.T) {
	handle := open(t)

	changed := errors.New("the caller changed its mind")
	err := handle.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&order{ID: "o1", Note: "rolled back"}).Error; err != nil {
			return err
		}
		return changed
	})
	if !errors.Is(err, changed) {
		t.Fatalf("the transaction gave %v, want the caller's own error", err)
	}
	if got := len(auditRows(t, handle)); got != 0 {
		t.Fatalf("a rolled-back write left %d audit row(s) behind", got)
	}

	if err := handle.Transaction(func(tx *gorm.DB) error {
		return tx.Create(&order{ID: "o2", Note: "committed"}).Error
	}); err != nil {
		t.Fatalf("committing: %v", err)
	}
	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want the committed write recorded, got %d row(s)", len(rows))
	}
	if rows[0].Outcome != "ok" {
		t.Errorf("outcome is %q, want %q", rows[0].Outcome, "ok")
	}
}

// A bare write and its record fail together.
//
// This is the path a host writes most of its code on: no transaction of its
// own, so the one GORM opens around the statement is the one that matters.
// A callback anchored on the statement's own step rather than on the commit
// runs after that transaction has ended: the row goes out on a second
// connection, and a failure is raised against a statement that has committed,
// telling the caller to retry a write that is already durable.
func TestABareWriteFailsWithItsRecord(t *testing.T) {
	handle := sharedMemory(t)
	if installErr := handle.Use(newCapture()); installErr != nil {
		t.Fatalf("installing the capture plugin: %v", installErr)
	}
	if createErr := handle.Exec(createOrders).Error; createErr != nil {
		t.Fatalf("creating the orders table: %v", createErr)
	}
	// No audit table, so every capture write fails.

	err := handle.Create(&order{ID: "o1", Note: "no trail"}).Error
	if !errors.Is(err, audit.ErrWriteFailed) {
		t.Fatalf("the write gave %v, want ErrWriteFailed", err)
	}

	var committed int64
	if err := handle.Model(&order{}).Count(&committed).Error; err != nil {
		t.Fatalf("counting: %v", err)
	}
	if committed != 0 {
		t.Fatalf("the caller was told the statement failed, but %d business row(s) committed", committed)
	}
}

// Raw SQL reaches no audit row.
//
// The plugin registers on the model-path processors only, so a statement issued
// through Exec is not merely unrecorded — it never reaches the callback. That is
// the reach the design states as a non-guarantee, and it is also what keeps a
// migration or a host's own cleanup from recording itself.
func TestRawSQLIsNotCaptured(t *testing.T) {
	handle := open(t)
	if err := handle.Exec("INSERT INTO orders (id, note) VALUES (?, ?)", "o1", "raw").Error; err != nil {
		t.Fatalf("writing the order: %v", err)
	}
	if got := len(auditRows(t, handle)); got != 0 {
		t.Fatalf("a raw SQL write was captured: %d audit row(s)", got)
	}
}

// The masked set is exactly the closed one, spelled exactly.
//
// A wider match — case-insensitive, or a suffix — would mask values whose names
// merely resemble these, and a reader who found a masked value could no longer
// tell which rule had caught it.
func TestMaskDetailCoversTheClosedSetAndNothingElse(t *testing.T) {
	in := map[string]string{
		"password":      "hunter2",
		"secret":        "s",
		"token":         "t",
		"credential":    "c",
		"authorization": "a",
		"Password":      "capitalised",
		"a.password":    "dotted",
		"note":          "kept",
	}
	out := maskDetail(in)

	for _, key := range []string{"password", "secret", "token", "credential", "authorization"} {
		if out[key] != log.MaskedText {
			t.Errorf("%s came back as %q, want %q", key, out[key], log.MaskedText)
		}
	}
	for _, key := range []string{"Password", "a.password", "note"} {
		if out[key] != in[key] {
			t.Errorf("%s came back as %q, want it left alone at %q", key, out[key], in[key])
		}
	}
	if in["password"] != "hunter2" {
		t.Error("the caller's own map was written through")
	}
}

// The row is assembled from a copy of the caller's detail, so a caller that
// keeps its map and edits it afterwards does not reach a row already written.
func TestAssembleCopiesTheCallersDetail(t *testing.T) {
	detail := map[string]string{"note": "before"}
	got := assemble(context.Background(), audit.SourceSeam, audit.Entry{Detail: detail}, "")
	detail["note"] = "after"
	if !strings.Contains(got.Detail, "before") {
		t.Errorf("the assembled row followed the caller's edit: %s", got.Detail)
	}
}

// One fault produces one line, and its end produces one more.
func TestSignalReportsOncePerFault(t *testing.T) {
	var buf bytes.Buffer
	s := &signal{out: &buf}

	s.fail(errors.New("first"))
	s.fail(errors.New("second"))
	s.fail(errors.New("third"))
	s.recover()
	s.recover()

	got := buf.String()
	if lines := strings.Count(got, "\n"); lines != 2 {
		t.Fatalf("want a failure line and a recovery line, got %d:\n%s", lines, got)
	}
	if !strings.Contains(got, "recovered after 3 failed") {
		t.Errorf("the recovery line does not account for the fault:\n%s", got)
	}
}

// The seam returns its failure; a capture path does not.
//
// The handle has no audit table, so every write fails. Record says so, which is
// what a caller asked for; the request path reports and carries on, which is
// what keeps a request from failing on account of the record of it.
func TestTheSeamReturnsItsFailureAndACapturePathDoesNot(t *testing.T) {
	// A handle with no audit table, so every write fails.
	handle := sharedMemory(t)
	var reported bytes.Buffer
	j := newJournal(settings{}, handle, nil)
	j.signal = &signal{out: &reported}
	j.accepting.Store(true)

	// A capture path reports the failure without raising it. The signal is what
	// makes that observable: without this assertion the case passes whether
	// submit reports, swallows, or panics.
	ctx := context.Background()
	j.submit(ctx, assemble(ctx, audit.SourceRequest, audit.Entry{Action: "a"}, ""))
	if reported.Len() == 0 {
		t.Error("a capture path's write failure was not reported anywhere")
	}

	// The seam says so too, which is what a caller asked for — and it says so
	// without adding a second line for the same standing fault.
	after := reported.Len()
	if err := j.Record(context.Background(), audit.Entry{Action: "a"}); !errors.Is(err, audit.ErrWriteFailed) {
		t.Fatalf("Record gave %v, want ErrWriteFailed", err)
	}
	if reported.Len() != after {
		t.Error("a failure while one already stood produced a second line")
	}
}

// The seam is behind the same gate as the paths the module drives.
func TestTheSeamIsBehindTheGate(t *testing.T) {
	handle := open(t)
	j := newJournal(settings{}, handle, nil)
	j.signal = &signal{out: io.Discard}

	if err := j.Record(context.Background(), audit.Entry{Action: "before"}); !errors.Is(err, audit.ErrNotAccepting) {
		t.Fatalf("Record before Start gave %v, want ErrNotAccepting", err)
	}
	j.accepting.Store(true)

	if err := j.Record(context.Background(), audit.Entry{Action: "during"}); err != nil {
		t.Fatalf("Record after Start gave %v, want nil", err)
	}
	j.accepting.Store(false)

	if err := j.Record(context.Background(), audit.Entry{Action: "after"}); !errors.Is(err, audit.ErrNotAccepting) {
		t.Fatalf("Record after Stop gave %v, want ErrNotAccepting", err)
	}

	rows := auditRows(t, handle)
	if len(rows) != 1 || rows[0].Action != "during" {
		t.Fatalf("want the one record taken while accepting, got %+v", rows)
	}
}

// The request path is behind the same gate as the paths the module drives: a
// row for a request that finishes after Stop is not written. The control is the
// same call while the journal is accepting, which writes it.
func TestARequestRowIsNotWrittenOnceTheJournalStoppedAccepting(t *testing.T) {
	handle := open(t)
	j := newTestJournal(handle, settings{})
	request := httptest.NewRequest("GET", "/x", nil)

	j.recordRequest(request, "public", "/x", "200")
	if got := len(auditRows(t, handle)); got != 1 {
		t.Fatalf("an accepting journal wrote %d request row(s), want 1", got)
	}

	j.accepting.Store(false)
	j.recordRequest(request, "public", "/x", "200")
	if got := len(auditRows(t, handle)); got != 1 {
		t.Errorf("a stopped journal wrote a request row: %d row(s), want the one written while accepting", got)
	}
}

// The capture layer stands for the audit capability on the chain and sits in
// the recording stage: the token and the stage are what other layers place
// themselves against.
func TestMiddlewareCarriesTheAuditToken(t *testing.T) {
	j := newJournal(settings{}, nil, nil)
	mw := j.middlewareFor("public")
	if mw.Name != ModuleName {
		t.Errorf("layer name is %q, want %q", mw.Name, ModuleName)
	}
	if len(mw.Provides) != 1 || mw.Provides[0] != (*audit.Audit)(nil) {
		t.Errorf("the layer provides %v, want the audit token", mw.Provides)
	}
	if mw.Stage != speedhttp.StageRecord {
		t.Errorf("the layer is in stage %v, want the recording stage", mw.Stage)
	}
	if mw.Handler == nil {
		t.Error("the layer has no Handler")
	}
}

// A subscription records the type it was made for, with what the payload says
// about the fact and the tenant the bus carried.
func TestEventPathRecordsASubscribedType(t *testing.T) {
	handle := open(t)
	bus := newRecordingBus()
	j := newTestJournal(handle, settings{events: []string{"order.updated"}})
	j.subscribe(bus)

	if err := bus.Publish(context.Background(), event.Event{
		Type:     "order.updated",
		TenantID: "acme",
		Payload: map[string]any{
			"object":  "order/o1",
			"outcome": "ok",
			"note":    "shipped",
		},
	}); err != nil {
		t.Fatalf("publishing: %v", err)
	}

	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want one row for the subscribed type, got %d", len(rows))
	}
	got := rows[0]
	if got.Source != string(audit.SourceEvent) {
		t.Errorf("source is %q, want %q", got.Source, audit.SourceEvent)
	}
	if got.Action != "order.updated" {
		t.Errorf("action is %q, want the type name", got.Action)
	}
	if got.Object != "order/o1" || got.Outcome != "ok" {
		t.Errorf("object/outcome are %q/%q, want the payload's", got.Object, got.Outcome)
	}
	// The tenant arrives on the event, not in a context a subscription callback
	// has none of.
	if got.TenantID == nil || *got.TenantID != "acme" {
		t.Errorf("tenant is %v, want the one the event carried", got.TenantID)
	}
	if !strings.Contains(got.Detail, `"note":"shipped"`) {
		t.Errorf("the payload's string fields are not in the detail: %s", got.Detail)
	}
}

// A type nobody subscribed to is not recorded, and a payload that cannot be
// read costs the event rather than the row.
func TestEventPathIgnoresUnsubscribedTypesAndUnreadablePayloads(t *testing.T) {
	handle := open(t)
	bus := newRecordingBus()
	j := newTestJournal(handle, settings{events: []string{"order.updated"}})
	j.subscribe(bus)

	if err := bus.Publish(context.Background(), event.Event{Type: "order.deleted"}); err != nil {
		t.Fatalf("publishing an unsubscribed type: %v", err)
	}
	if got := len(auditRows(t, handle)); got != 0 {
		t.Fatalf("an unsubscribed type was recorded: %d row(s)", got)
	}

	// A payload that cannot be decoded to an object. The subscriber's contract
	// with its publisher is to drop what it cannot read.
	if err := bus.Publish(context.Background(), event.Event{
		Type:    "order.updated",
		Payload: make(chan int),
	}); err != nil {
		t.Fatalf("publishing an unreadable payload: %v", err)
	}
	if got := len(auditRows(t, handle)); got != 0 {
		t.Fatalf("an unreadable payload was recorded: %d row(s)", got)
	}
}

// A type named twice is one subscription, so the fact is recorded once.
func TestEventPathSubscribesOncePerType(t *testing.T) {
	handle := open(t)
	bus := newRecordingBus()
	j := newTestJournal(handle, settings{events: []string{"order.updated", "order.updated"}})
	j.subscribe(bus)

	if err := bus.Publish(context.Background(), event.Event{Type: "order.updated"}); err != nil {
		t.Fatalf("publishing: %v", err)
	}
	if got := len(auditRows(t, handle)); got != 1 {
		t.Fatalf("a repeated type produced %d row(s), want 1", got)
	}
}

// Updates and deletes are captured the way inserts are, each under its own
// verb — the callbacks are three separate registrations and one of them
// standing in for another would go unnoticed here otherwise.
func TestStatementCaptureRecordsUpdateAndDelete(t *testing.T) {
	handle := open(t)

	if err := handle.Create(&order{ID: "o1", Note: "first"}).Error; err != nil {
		t.Fatalf("creating the order: %v", err)
	}
	if err := handle.Model(&order{}).Where("id = ?", "o1").Update("note", "second").Error; err != nil {
		t.Fatalf("updating the order: %v", err)
	}
	if err := handle.Delete(&order{}, "id = ?", "o1").Error; err != nil {
		t.Fatalf("deleting the order: %v", err)
	}

	rows := auditRows(t, handle)
	verbs := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Object != "orders" {
			t.Errorf("a statement was recorded against %q, want %q", r.Object, "orders")
		}
		verbs = append(verbs, r.Action)
	}
	if want := "create,update,delete"; strings.Join(verbs, ",") != want {
		t.Fatalf("the verbs are %q, want %q", strings.Join(verbs, ","), want)
	}
}

// A failing Prepare hands back the zero Enablement.
//
// Only a Prepare that returned no error has its stance read, so what travels
// with the error is the zero value — the convention every other module states a
// stance under. The stance this module states on the way through is StateEnabled
// and never StateAuto: the capability is delivered exclusively, and an exclusive
// provider stating StateAuto is the one cell resolution disables.
func TestPrepareReturnsZeroEnablementWhenItCannotStart(t *testing.T) {
	// Nothing is supplied, so the run carries no configuration reader for
	// this module to read its own section with.
	reg := core.New()
	stated, err := Module().Prepare(context.Background(), reg)
	if err == nil {
		t.Fatal("Prepare accepted a run carrying no configuration reader")
	}
	if stated != (core.Enablement{}) {
		t.Errorf("Prepare handed back %+v beside its failure, want the zero Enablement: a stance "+
			"is read only from a Prepare that returned no error", stated)
	}
	if !strings.Contains(err.Error(), "configuration reader") {
		t.Errorf("the failure does not name what is absent: %v", err)
	}
}

// suppliedAudit is a value a host can supply for this module: it delivers the
// audit capability and is not the module's product.
type suppliedAudit struct{}

func (suppliedAudit) Record(context.Context, audit.Entry) error { return nil }

// The callbacks that act on the module's product leave an instance a host
// supplied for the module as it is.
//
// The registry hands a callback whatever instance the module has, and a host can
// supply any value that delivers the audit capability. A callback that took its
// instance for the journal would panic on it, or fail the startup for it. The
// contrast is the module's own product, which the same callbacks act on in the
// same registry: Init fails for it, because the configuration names an endpoint
// and the registry has no entry point to serve it, and Start and Stop switch what
// it accepts.
func TestTheCallbacksLeaveASuppliedInstanceAlone(t *testing.T) {
	ctx := context.Background()
	module := Module()
	reg := core.New()

	for _, callback := range []struct {
		name string
		call func(context.Context, *core.Registry, any) error
	}{
		{"Init", module.Init},
		{"Start", module.Start},
		{"Stop", module.Stop},
		{"Close", module.Close},
	} {
		if err := callback.call(ctx, reg, suppliedAudit{}); err != nil {
			t.Errorf("%s on an instance the host supplied gave %v, want it left alone", callback.name, err)
		}
	}

	own := newJournal(settings{endpoints: []string{"public"}}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := module.Init(ctx, reg, own); err == nil {
		t.Error("Init on the module's own product gave no error in a registry with no entry point, " +
			"so the calls above show nothing about what Init does to an instance")
	}
	if err := module.Start(ctx, reg, own); err != nil || !own.accepting.Load() {
		t.Errorf("Start on the module's own product gave %v with accepting %v, want it accepting",
			err, own.accepting.Load())
	}
	if err := module.Stop(ctx, reg, own); err != nil || own.accepting.Load() {
		t.Errorf("Stop on the module's own product gave %v with accepting %v, want it stopped",
			err, own.accepting.Load())
	}
}

// A configuration naming event types fails the startup when the run carries no
// bus, and the failure names the input item that asked for it.
func TestWireFailsWhenEventsAreNamedAndNoBusIsInTheRun(t *testing.T) {
	j := newTestJournal(open(t), settings{events: []string{"thing.changed"}})
	err := j.wire(context.Background(), core.New())
	if err == nil {
		t.Fatal("wiring accepted event types named with no bus in the run")
	}
	if !strings.Contains(err.Error(), eventsItem) {
		t.Errorf("the failure does not name the %s input item: %v", eventsItem, err)
	}
}

// Naming an endpoint without an entry point in the run fails the startup too,
// rather than leaving the layer off the endpoint in silence.
func TestWireFailsWhenEndpointsAreNamedAndNoRouterIsInTheRun(t *testing.T) {
	j := newTestJournal(open(t), settings{endpoints: []string{"public"}})
	err := j.wire(context.Background(), core.New())
	if err == nil {
		t.Fatal("wiring accepted an endpoint named with no entry point in the run")
	}
	if !strings.Contains(err.Error(), endpointsItem) {
		t.Errorf("the failure does not name the %s input item: %v", endpointsItem, err)
	}
}

// A configuration naming nothing wires cleanly: the control the two failures
// above are read against.
func TestWireSucceedsWhenNothingIsNamed(t *testing.T) {
	j := newTestJournal(open(t), settings{})
	if err := j.wire(context.Background(), core.New()); err != nil {
		t.Fatalf("wiring a configuration that names nothing: %v", err)
	}
}
