package journal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/audit"
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

// auditMigration is the migration the module ships for the dialect these cases
// run on. The table the cases write into is built from it rather than from a
// copy written out here: a copy is a second description of the schema, and the
// two part company at the first column someone adds.
const auditMigration = "sqlite/0001_audit_entries.sql"

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

// quiet is the configuration every handle in these tests opens with. GORM
// prints a statement that failed, and two of these cases fail a statement on
// purpose; the failure is the point, and the output would only be noise.
func quiet() *gorm.Config {
	return &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
}

// open builds a handle with the capture plugin installed and both tables
// created. The tables are created with raw SQL, which the plugin does not
// capture — reaching the plugin needs the model path.
func open(t *testing.T) *gorm.DB {
	t.Helper()
	handle, err := gorm.Open(sqlitedriver.Open("file:"+t.Name()+"?mode=memory&cache=shared"), quiet())
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	if err := handle.Use(newCapture()); err != nil {
		t.Fatalf("installing the capture plugin: %v", err)
	}
	if err := handle.Exec(createOrders).Error; err != nil {
		t.Fatalf("creating the orders table: %v", err)
	}
	applyMigration(t, handle, auditMigration)
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

// A configured pattern matches the way the routing engine matches it.
func TestMatcherReadsPatternsLikeTheEngine(t *testing.T) {
	m, err := newMatcher([]string{"GET /things", "POST /things/{id}"})
	if err != nil {
		t.Fatalf("building the matcher: %v", err)
	}
	cases := []struct {
		method, path string
		want         string
		matched      bool
	}{
		{"GET", "/things", "GET /things", true},
		{"GET", "/things/1", "", false},
		{"POST", "/things/1", "POST /things/{id}", true},
		{"GET", "/things/1", "", false},
		{"DELETE", "/things", "", false},
	}
	for _, c := range cases {
		got, ok := m.match(httptest.NewRequest(c.method, c.path, nil))
		if ok != c.matched || got != c.want {
			t.Errorf("%s %s matched (%q, %v), want (%q, %v)", c.method, c.path, got, ok, c.want, c.matched)
		}
	}
}

// A pattern the matcher cannot read is refused at build time rather than at
// some later request.
func TestMatcherRefusesAPatternItCannotRead(t *testing.T) {
	if _, err := newMatcher([]string{"GET /{"}); !errors.Is(err, ErrUnmatchablePattern) {
		t.Fatalf("building a matcher over a malformed pattern gave %v, want ErrUnmatchablePattern", err)
	}
}

// A repeated pattern is one pattern. Passing the duplicate through to the
// router would panic, and a panic in the Init stage skips the registry's
// rollback.
func TestMatcherAcceptsARepeatedPattern(t *testing.T) {
	m, err := newMatcher([]string{"GET /things", "GET /things"})
	if err != nil {
		t.Fatalf("building the matcher: %v", err)
	}
	if _, ok := m.match(httptest.NewRequest("GET", "/things", nil)); !ok {
		t.Fatal("the repeated pattern did not match")
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
	handle, err := gorm.Open(sqlitedriver.Open("file:"+t.Name()+"?mode=memory&cache=shared"), quiet())
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	var reported bytes.Buffer
	j := newJournal(settings{}, handle, nil)
	j.signal = &signal{out: &reported}
	j.accepting.Store(true)

	// A capture path reports the failure without raising it. The signal is what
	// makes that observable: without this assertion the case passes whether
	// submit reports, swallows, or panics.
	j.submit(context.Background(), audit.SourceRequest, audit.Entry{Action: "a"})
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

// The request path writes what the design says it writes: the method as the
// action, the matched pattern as the object, and the answer as the outcome.
func TestMatchedRequestRecordsTheOutcome(t *testing.T) {
	handle := open(t)
	j := newJournal(settings{}, handle, nil)
	j.signal = &signal{out: io.Discard}
	j.accepting.Store(true)
	m, err := newMatcher([]string{"GET /things"})
	if err != nil {
		t.Fatalf("building the matcher: %v", err)
	}
	j.matcher = m

	inner := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusTeapot)
	})
	layer := j.middlewareFor("public").Wrap(inner)
	layer.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/things", nil))

	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want one row, got %d", len(rows))
	}
	got := rows[0]
	if got.Source != string(audit.SourceRequest) || got.Action != "GET" ||
		got.Object != "GET /things" || got.Outcome != "418" {
		t.Errorf("unexpected row: %+v", got)
	}
	if !strings.Contains(got.Detail, `"endpoint":"public"`) {
		t.Errorf("the endpoint is not in the detail: %s", got.Detail)
	}
}

// A handler that panics is still recorded, and the panic is still thrown.
func TestPanickingHandlerIsRecordedAndRethrown(t *testing.T) {
	handle := open(t)
	j := newJournal(settings{}, handle, nil)
	j.signal = &signal{out: io.Discard}
	j.accepting.Store(true)
	m, err := newMatcher([]string{"GET /things"})
	if err != nil {
		t.Fatalf("building the matcher: %v", err)
	}
	j.matcher = m

	inner := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
		panic("the handler fell over")
	})
	layer := j.middlewareFor("public").Wrap(inner)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed rather than thrown again")
			}
		}()
		layer.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/things", nil))
	}()

	rows := auditRows(t, handle)
	if len(rows) != 1 {
		t.Fatalf("want the panicking request recorded, got %d row(s)", len(rows))
	}
	if rows[0].Outcome != panicOutcome {
		t.Errorf("outcome is %q, want %q", rows[0].Outcome, panicOutcome)
	}
}

// The capture layer is not registered when nothing names its requests, and the
// two After edges are the ones other layers anchor against.
func TestMiddlewareCarriesTheAuditToken(t *testing.T) {
	j := newJournal(settings{}, nil, nil)
	mw := j.middlewareFor("public")
	if mw.Name != ModuleName {
		t.Errorf("layer name is %q, want %q", mw.Name, ModuleName)
	}
	if len(mw.Provides) != 1 || mw.Provides[0] != (*audit.Audit)(nil) {
		t.Errorf("the layer provides %v, want the audit token", mw.Provides)
	}
	if len(mw.After) != 2 {
		t.Errorf("the layer sits after %v, want authentication and tenant resolution", mw.After)
	}
	if mw.Wrap == nil {
		t.Error("the layer has no Wrap")
	}
}

// A request that matches no configured pattern is passed through untouched and
// records nothing, which is what makes an unwritten endpoint silent rather than
// noisy.
func TestUnmatchedRequestIsPassedThrough(t *testing.T) {
	var served bool
	j := newJournal(settings{}, nil, nil)
	j.signal = &signal{out: io.Discard}
	m, err := newMatcher([]string{"GET /things"})
	if err != nil {
		t.Fatalf("building the matcher: %v", err)
	}
	j.matcher = m

	inner := nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { served = true })
	layer := j.middlewareFor("public").Wrap(inner)
	layer.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/other", nil))

	if !served {
		t.Error("the inner handler never ran")
	}
}
