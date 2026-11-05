package catalog

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/tenant"
)

// databaseOver is a delivered handle a case can build: the roster reaches the
// database only through this interface, and a case needs nothing of it beyond
// the handle.
type databaseOver struct{ handle *gorm.DB }

func (d databaseOver) DB() *gorm.DB             { return d.handle }
func (d databaseOver) Dialect() db.Dialect      { return db.SQLite }
func (d databaseOver) BlindIndex(string) string { return "" }
func (d databaseOver) Open(context.Context, string) (*gorm.DB, error) {
	return nil, errors.New("this case opens no second connection")
}

// rosterOver builds a roster over a table of its own, with the rows written
// into it. The handle speaks SQLite, so the schema is the migration the module
// ships for that dialect.
func rosterOver(t *testing.T, rows ...string) *roster {
	t.Helper()
	return rosterOverDialect(t, "sqlite", rows...)
}

// openHandle is the handle a case builds its roster over: the one the database
// module delivers over a SQLite file of the case's own, which every connection
// of the pool shares. It is the module's handle rather than one opened here, so
// that the roster runs over what an assembly hands it: the constraint
// classification the module installs, and the pool shape it gives the locator.
//
// The query log is silenced on the session handed back: a case that makes a
// read fail on purpose asserts on the error that came back, and GORM's echo of
// that same failure is output rather than a result.
func openHandle(t *testing.T) *gorm.DB {
	t.Helper()
	return openHandleAt(t, filepath.Join(t.TempDir(), "roster.db"))
}

// openHandleAt is openHandle over the SQLite locator dsn.
func openHandleAt(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	return quiet(dbtest.SQLite(t, dsn, nil).DB())
}

// quiet is a session of a delivered handle that writes no record of a failed
// statement.
func quiet(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}

// rosterOverDialect is rosterOver with the dialect named, so that a case can
// build the table from any dialect's migration rather than only the one this
// fixture's handle speaks.
//
// The rows arrive as statements rather than through Put, which is the point:
// what these cases are about is a row that reached the table by some other
// route, and Put is the one route that would have refused it. The schema comes
// from the migration file itself, so a case cannot drift from the table the
// module really reads.
//
// The database is a file of the case's own, and its pool is closed when the
// case ends.
func rosterOverDialect(t *testing.T, dialect string, rows ...string) *roster {
	t.Helper()
	return rosterOn(t, openHandle(t), dialect, rows...)
}

// applyMigrations builds the roster's table from the files the module ships for
// one dialect, in filename order.
//
// A dialect's migration set is every .sql file in its directory: the first
// creates the table and a later one adds the columns this build reads, so a
// fixture that applied only the first would build a table the roster no longer
// matches.
func applyMigrations(t *testing.T, handle *gorm.DB, dialect string) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, dialect)
	if err != nil {
		t.Fatalf("listing the %s migrations: %v", dialect, err)
	}
	if len(entries) == 0 {
		t.Fatalf("the %s migration set is empty", dialect)
	}
	for _, entry := range entries {
		ddl, err := fs.ReadFile(migrationsFS, dialect+"/"+entry.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		if err := handle.Exec(string(ddl)).Error; err != nil {
			t.Fatalf("applying %s: %v", entry.Name(), err)
		}
	}
}

// rosterOn is rosterOverDialect over a handle the case opened.
func rosterOn(t *testing.T, handle *gorm.DB, dialect string, rows ...string) *roster {
	t.Helper()
	applyMigrations(t, handle, dialect)
	for _, stmt := range rows {
		if err := handle.Exec(stmt).Error; err != nil {
			t.Fatalf("writing %q: %v", stmt, err)
		}
	}
	return newRoster(settings{}, databaseOver{handle: handle})
}

// insert is one roster row written as a statement.
func insert(id, alias string) string {
	return "INSERT INTO tenants (id, alias, status) VALUES ('" + id + "', '" + alias + "', 1)"
}

// TestReadRefusesARowOutsideTheIdentifierDomain is the read-path half of the
// domain. Put refuses such a tenant, but a table can be written without Put —
// a statement run by hand, an import, a database this module shares — and what
// such a row carries is then handed to every caller that resolves a tenant,
// one of which refuses an out-of-domain identifier by panicking.
//
// The read refuses the row rather than leaving it out of the snapshot: a
// snapshot that quietly omitted a tenant would answer every request for it as
// one that does not exist, and nothing would say so.
func TestReadRefusesARowOutsideTheIdentifierDomain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		row   string
		field string
	}{
		{"a blank inside the identifier", insert("acme corp", "acme"), "identifier"},
		{"a control character in the identifier", insert(`acme`+"\n"+`corp`, "acme"), "identifier"},
		{"an empty identifier", insert("", "acme"), "identifier"},
		{"a blank inside the alias", insert("acme", "ac me"), "alias"},
		{"a control character in the alias", insert("acme", `ac`+"\t"+`me`), "alias"},
		{"an empty alias", insert("acme", ""), "alias"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rosterOver(t, insert("globex", "globex"), tc.row)
			err := r.read(context.Background())
			if err == nil {
				t.Fatal("the read published a table holding a tenant this module will not act on")
			}
			if !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("the failure is %v, want it to carry ErrInvalidTenant", err)
			}
			if text := err.Error(); !strings.Contains(text, tableName) || !strings.Contains(text, tc.field) {
				t.Errorf("the failure names neither the table (%q) nor the field (%q): %v",
					tableName, tc.field, err)
			}
			// The value is what the domain exists to keep out of log lines and
			// error text, so the failure that reports a bad row must not carry
			// it — a newline in the message would split the record in two.
			if text := err.Error(); strings.ContainsAny(text, "\n\t\x00") {
				t.Errorf("the failure carries a control character of its own: %q", text)
			}
			if r.snap.Load() != nil {
				t.Error("the read published a snapshot although it refused the table")
			}
		})
	}
}

// TestReadPublishesAConformingRoster is the other side of the same check: the
// predicate is the domain's, and a table inside it loads.
//
// One of the two identifiers is long. This module defines no length for one, so
// it has none to refuse, and a case that started refusing a long identifier
// would fail here.
func TestReadPublishesAConformingRoster(t *testing.T) {
	r := rosterOver(t,
		insert("acme", "acme"),
		insert(strings.Repeat("a", 200), "globex"),
	)
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading a table inside the domain: %v", err)
	}
	s, err := r.current()
	if err != nil {
		t.Fatalf("the read published nothing: %v", err)
	}
	if _, ok := s.byAlias["acme"]; !ok {
		t.Error("the snapshot does not hold the tenant the table carries")
	}
	if _, ok := s.byAlias["globex"]; !ok {
		t.Error("the snapshot does not hold the tenant with a long identifier, which this module " +
			"has no reason to refuse")
	}
}

// TestReadLeavesThePreviousSnapshotInPlaceWhenItRefuses is what the refusal
// costs and what it buys. A refresh that finds a bad row keeps serving the
// snapshot it already had, so the tenant that turned bad stops being resolved
// rather than being resolved to an identifier a consumer refuses.
func TestReadLeavesThePreviousSnapshotInPlaceWhenItRefuses(t *testing.T) {
	r := rosterOver(t, insert("acme", "acme"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	before, err := r.current()
	if err != nil {
		t.Fatalf("the first read published nothing: %v", err)
	}

	if err = r.handle.Exec(`UPDATE tenants SET id = 'acme corp' WHERE alias = 'acme'`).Error; err != nil {
		t.Fatalf("turning the row bad: %v", err)
	}
	if err = r.read(context.Background()); err == nil {
		t.Fatal("the reread published a table holding a tenant this module will not act on")
	}
	after, err := r.current()
	if err != nil {
		t.Fatalf("the previous snapshot was dropped: %v", err)
	}
	if after != before {
		t.Error("the refused read replaced the snapshot instead of leaving the previous one in place")
	}
	if _, ok := after.byAlias["acme"]; !ok {
		t.Error("the previous snapshot no longer answers for the tenant it held")
	}
}

// rosterWithoutTable builds a roster over a handle whose table was never
// created. A deployment that never applied this module's migration reaches the
// read with no table, which is the condition the load has to report rather than
// read as an empty roster.
func rosterWithoutTable(t *testing.T) *roster {
	t.Helper()
	return newRoster(settings{}, databaseOver{handle: openHandle(t)})
}

// requireRosterMissingOnLoad holds one load's failure to the sentinel and the
// table name the design promises it carries.
func requireRosterMissingOnLoad(t *testing.T, r *roster) {
	t.Helper()
	err := r.load(context.Background())
	if err == nil {
		t.Fatal("the load returned nil for a table that is not there, so the startup would continue " +
			"against an empty roster")
	}
	if !errors.Is(err, tenant.ErrRosterMissing) {
		t.Fatalf("the failure is %v, want it to carry ErrRosterMissing", err)
	}
	if text := err.Error(); !strings.Contains(text, tableName) {
		t.Errorf("the failure does not name the table (%q): %v", tableName, err)
	}
}

// TestLoadEndsTheStartupWhenTheTableIsNotThere holds the sentinel the design
// names for a table that is not there: ErrRosterMissing is reported when the
// snapshot is loaded, ends the startup, and names the table. It is the failure
// with no other repair than applying the migration set, so a load that reported
// it as anything else — or as nothing at all — would leave a deployment serving
// an empty roster while telling the operator to look elsewhere.
//
// Both routes to a missing table are covered: one that was never created, and
// one dropped from under a roster that has already loaded.
func TestLoadEndsTheStartupWhenTheTableIsNotThere(t *testing.T) {
	t.Run("never created", func(t *testing.T) {
		requireRosterMissingOnLoad(t, rosterWithoutTable(t))
	})
	t.Run("dropped after a load", func(t *testing.T) {
		r := rosterOver(t, insert("acme", "acme"))
		// The startup account the successful load writes is not what this case
		// is about, so it is read into a handler that keeps it rather than left
		// on the process's own logger.
		captureLogs(r)
		if err := r.load(context.Background()); err != nil {
			t.Fatalf("loading a conforming roster: %v", err)
		}
		if err := r.handle.Exec("DROP TABLE " + tableName).Error; err != nil {
			t.Fatalf("dropping the table: %v", err)
		}
		requireRosterMissingOnLoad(t, r)
	})
}

// requireUnreadRefusal holds one read method of a roster that has not been read
// to a refusal rather than the empty answer. The empty answer — a zero tenant
// with found false and no error — is what a caller reads as "no tenant carries
// this", so a method that returned it would turn every request into a 404 for a
// roster that has not yet looked at the table.
func requireUnreadRefusal(t *testing.T, method string, got tenant.Tenant, found bool, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s answered a roster that has not been read: %+v (found=%v), want a failure so that "+
			"a caller cannot read an empty answer as a tenant that does not exist", method, got, found)
	}
	if found || got != (tenant.Tenant{}) {
		t.Errorf("%s reported the tenant %+v (found=%v) on a roster that has not been read",
			method, got, found)
	}
	// A caller decides "no such tenant" from ErrInvalidTenant on the paths that
	// report it, so the refusal must not be that failure.
	if errors.Is(err, tenant.ErrInvalidTenant) {
		t.Errorf("the failure %s returns carries ErrInvalidTenant, which a caller reads as a tenant "+
			"that is not there: %v", method, err)
	}
}

// TestAnUnreadRosterRefusesTheReads holds the whole read face to a refusal
// before the first read has published a snapshot. A consumer that reached the
// roster before its load — an ordering the module cannot rule out for the host
// — must not be answered with the empty result, because the empty result is
// indistinguishable from the roster saying a tenant does not exist.
//
// All three read methods go through the same refusal, so all three are held,
// each to the same two things: an error rather than the empty answer, and one
// that is not the sentinel a caller reads as "no such tenant".
func TestAnUnreadRosterRefusesTheReads(t *testing.T) {
	r := rosterOver(t)
	ctx := context.Background()

	got, found, err := r.Get(ctx, "acme")
	requireUnreadRefusal(t, "Get", got, found, err)

	byAlias, found, err := r.ByAlias(ctx, "acme")
	requireUnreadRefusal(t, "ByAlias", byAlias, found, err)

	listed, err := r.List(ctx)
	if err == nil {
		t.Fatalf("List answered a roster that has not been read with %d tenants, want a failure",
			len(listed))
	}
	if len(listed) != 0 {
		t.Errorf("List returned %d tenants on a roster that has not been read", len(listed))
	}
	if errors.Is(err, tenant.ErrInvalidTenant) {
		t.Errorf("the failure List returns carries ErrInvalidTenant, which a caller reads as a "+
			"tenant that is not there: %v", err)
	}
}

// identifiersOf joins a listing's identifiers in the order the listing returned
// them, so that a case can state the order it wants as one value.
func identifiersOf(list []tenant.Tenant) string {
	ids := make([]string, len(list))
	for i, entry := range list {
		ids[i] = string(entry.ID)
	}
	return strings.Join(ids, ",")
}

// TestListIsOrderedByIdentifierAndIsACopy holds the two things List promises
// about what it hands out.
//
// The order is the module's, not the table's: the snapshot is sorted by
// identifier as it is built, so a listing does not depend on the order the rows
// happen to come back in. The rows here are written in an order of their own so
// that a listing which merely echoed the table's order would be told apart from
// one ordered by identifier. What is read is the identifier order that comes
// out, which is this promise's own expression: it is what a caller that wants a
// stable enumeration relies on, and comparing the tenants as a bag of values
// would hold nothing about it.
//
// The slice is a copy: a caller sorting or trimming what it was handed must not
// disturb what the next caller sees. Mutating the returned slice and listing
// again is what holds that half.
func TestListIsOrderedByIdentifierAndIsACopy(t *testing.T) {
	r := rosterOver(t,
		insert("carol", "carol"),
		insert("alice", "alice"),
		insert("bob", "bob"),
	)
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading a conforming roster: %v", err)
	}

	got, err := r.List(ctx)
	if err != nil {
		t.Fatalf("listing the roster: %v", err)
	}
	if held := identifiersOf(got); held != "alice,bob,carol" {
		t.Errorf("List returned the identifiers %q, want them ordered by identifier %q",
			held, "alice,bob,carol")
	}

	// A caller is free to reorder or replace what it was handed.
	if len(got) > 0 {
		got[0] = tenant.Tenant{ID: "zzz", Alias: "zzz", Status: tenant.StatusActive}
	}
	again, err := r.List(ctx)
	if err != nil {
		t.Fatalf("listing the roster a second time: %v", err)
	}
	if held := identifiersOf(again); held != "alice,bob,carol" {
		t.Errorf("a caller's change to the slice it was handed changed the next list: %q, want %q",
			held, "alice,bob,carol")
	}
}

// TestListOnALoadedTableWithNoTenants is the third answer a listing has, and
// the one a caller cannot get from the other two: a table that exists and holds
// nobody lists as nothing, with no failure, where a roster that has not been
// read refuses and one with tenants lists them. An operator's first run against
// a freshly migrated database is this shape, and a listing that failed there
// would report a fault where there is none.
func TestListOnALoadedTableWithNoTenants(t *testing.T) {
	r := rosterOver(t)
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading the table the migration makes: %v", err)
	}

	got, err := r.List(ctx)
	if err != nil {
		t.Fatalf("listing a roster whose table holds no tenant: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List returned %d tenants, want none: %v", len(got), got)
	}
}

// TestLookupsMissByAnsweringRatherThanFailing is the other side of the unread
// refusal: once the roster has been read, a name no row carries is an answer —
// no such tenant — and not a failure. A caller reads the two differently: a
// failure says the roster has not looked, a miss says it looked and the tenant
// is not there, and only the second is a fact about the table. Both lookups are
// held, because the resolving layer makes the alias lookup on every request and
// a miss there is what answers a request for a tenant that does not exist.
func TestLookupsMissByAnsweringRatherThanFailing(t *testing.T) {
	r := rosterOver(t, insert("acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading the roster: %v", err)
	}

	for _, lookup := range []struct {
		name string
		call func() (tenant.Tenant, bool, error)
	}{
		{"by identifier", func() (tenant.Tenant, bool, error) { return r.Get(ctx, "absent") }},
		{"by alias", func() (tenant.Tenant, bool, error) { return r.ByAlias(ctx, "absent") }},
	} {
		t.Run(lookup.name, func(t *testing.T) {
			found, ok, err := lookup.call()
			if err != nil {
				t.Fatalf("the lookup failed for a name no tenant carries: %v", err)
			}
			if ok {
				t.Error("the lookup reported a tenant for a name no row carries")
			}
			if found.ID != "" || found.Alias != "" {
				t.Errorf("the lookup returned %+v for a name no row carries, want the zero tenant", found)
			}
		})
	}
}

// countRows reports how many rows the roster's table holds, read back through
// the handle the case built and not through the snapshot. What a refused write
// left behind is a fact about the table: the predicate runs before the write,
// so a refusal and a row that was written and then failed to reread are told
// apart by the row count alone, both returning the same sentinel.
func countRows(t *testing.T, r *roster) int64 {
	t.Helper()
	var n int64
	if err := r.handle.Model(&row{}).Count(&n).Error; err != nil {
		t.Fatalf("counting the roster's rows: %v", err)
	}
	return n
}

// rowOf reads one roster row by identifier from the table the case built.
func rowOf(t *testing.T, r *roster, id string) (row, bool) {
	t.Helper()
	var held row
	err := r.handle.Where("id = ?", id).First(&held).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row{}, false
	}
	if err != nil {
		t.Fatalf("reading the row for %q: %v", id, err)
	}
	return held, true
}

// TestPutRefusesATenantOutsideTheIdentifierDomain is the write-path half of the
// domain: Put is the route a tenant is meant to enter the table by, and a
// blank, a control character or an empty value in either field is refused here.
//
// The row count is what holds the refusal to its placement. A put that wrote
// first and failed afterwards returns the same sentinel and names the same
// field — the reread it ends with refuses the whole table — so only the
// untouched table says the predicate ran before the write, which is what lets a
// caller retry with a corrected value instead of finding a row it did not want.
func TestPutRefusesATenantOutsideTheIdentifierDomain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		id    string
		alias string
		field string
	}{
		{"a blank inside the identifier", "acme corp", "acme", "identifier"},
		{"a control character in the identifier", "acme\ncorp", "acme", "identifier"},
		{"an empty identifier", "", "acme", "identifier"},
		{"a blank inside the alias", "acme", "ac me", "alias"},
		{"a control character in the alias", "acme", "ac\tme", "alias"},
		{"an empty alias", "acme", "", "alias"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rosterOver(t, insert("globex", "globex"))
			err := r.Put(context.Background(), tenant.Tenant{
				ID:     tenant.ID(tc.id),
				Alias:  tc.alias,
				Status: tenant.StatusActive,
			})
			if err == nil {
				t.Fatal("the write registered a tenant this module will not act on")
			}
			if !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("the failure is %v, want it to carry ErrInvalidTenant", err)
			}
			if text := err.Error(); !strings.Contains(text, tc.field) {
				t.Errorf("the failure does not name the field (%q): %v", tc.field, err)
			}
			// The value is what the domain exists to keep out of log lines and
			// error text, so the failure that reports a bad field must not carry
			// it — a newline in the message would split the record in two.
			if text := err.Error(); strings.ContainsAny(text, "\n\t\x00") {
				t.Errorf("the failure carries a control character of its own: %q", text)
			}
			if got := countRows(t, r); got != 1 {
				t.Errorf("the refused write left %d rows in the table, want the 1 it started with", got)
			}
		})
	}
}

// TestPutRewritesByIdentifier is Put's idempotence: the same identifier written
// twice is one row, carrying the second write's status. Both halves need
// holding — a put that inserted a second row and one that left the first row
// untouched are different defects that a claim of "one row" alone would not
// tell apart.
//
// The alias is resolved back through the roster rather than only read from the
// table: a rewrite whose result this process does not serve is one an operator
// has to wait out a refresh period to see. The alias stays the one the row was
// written with, and the second write names it so that the rewrite is not
// refused as a rename.
func TestPutRewritesByIdentifier(t *testing.T) {
	r := rosterOver(t)
	ctx := context.Background()

	if err := r.Put(ctx, tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("registering a tenant: %v", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Fatalf("the first write left %d rows, want 1", got)
	}

	if err := r.Put(ctx, tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusDisabled}); err != nil {
		t.Fatalf("rewriting the tenant: %v", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the second write left %d rows, want the 1 the first wrote", got)
	}
	held, ok := rowOf(t, r, "acme")
	if !ok {
		t.Fatal("the identifier the row was written under is no longer in the table")
	}
	if held.Alias != "acme" {
		t.Errorf("the row's alias is %q, want the %q it was written with", held.Alias, "acme")
	}
	if held.Status != int(tenant.StatusDisabled) {
		t.Errorf("the row's status is %d, want the second write's %d", held.Status, int(tenant.StatusDisabled))
	}
	if written, ok, err := r.ByAlias(ctx, "acme"); err != nil || !ok || written.Status != tenant.StatusDisabled {
		t.Errorf("the roster does not resolve the alias the row was written with: %+v (found=%v, err=%v)",
			written, ok, err)
	}
}

// TestPutRefusesToChangeAnAlias holds the alias's immutability: an alias is
// chosen when the row is written and is never changed afterwards, so a rewrite
// carrying another alias is refused and leaves the row as it was. Renaming
// would otherwise reopen the ambiguity the namespace check closes — a new alias
// could equal another tenant's identifier without any creation path seeing it.
func TestPutRefusesToChangeAnAlias(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	err := r.Put(context.Background(), tenant.Tenant{
		ID: "t_acme", Alias: "renamed", Status: tenant.StatusDisabled,
	})
	if !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("rewriting a tenant under another alias gave %v, want ErrInvalidTenant", err)
	}
	requireRow(t, r, "t_acme", row{ID: "t_acme", Alias: "acme", Status: int(tenant.StatusActive)})
}

// TestCreateRefusesAnAliasThatIsAnotherTenantsIdentifier and
// TestCreateRefusesAnIdentifierThatIsAnotherTenantsAlias hold the two halves of
// one namespace: a slug addresses one tenant whichever way it is looked up, so
// a creation cannot make one tenant's alias read as another tenant's
// identifier. Both refusals write nothing.
func TestCreateRefusesAnAliasThatIsAnotherTenantsIdentifier(t *testing.T) {
	r := rosterOver(t, insert("acme-one", "first"))
	err := r.Create(context.Background(), tenant.Tenant{
		ID: "second", Alias: "acme-one", Status: tenant.StatusActive,
	})
	if !errors.Is(err, tenant.ErrAliasConflict) {
		t.Fatalf("creating a tenant whose alias is another tenant's identifier gave %v, want ErrAliasConflict", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused creation left %d rows, want the 1 it started with", got)
	}
}

func TestCreateRefusesAnIdentifierThatIsAnotherTenantsAlias(t *testing.T) {
	r := rosterOver(t, insert("first", "second-alias"))
	err := r.Create(context.Background(), tenant.Tenant{
		ID: "second-alias", Alias: "third-alias", Status: tenant.StatusActive,
	})
	if !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("creating a tenant whose identifier is another tenant's alias gave %v, want ErrTenantExists", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused creation left %d rows, want the 1 it started with", got)
	}
}

// TestPutRefusesACrossNamespaceInsert holds the same two checks on Put's insert
// path: the rewrite is not the only way a row enters the table, so a creation
// through Put cannot leave the namespace ambiguous either.
func TestPutRefusesACrossNamespaceInsert(t *testing.T) {
	cases := []struct {
		name     string
		first    string
		second   tenant.Tenant
		sentinel error
	}{
		{
			name:     "an alias that is another tenant's identifier",
			first:    "INSERT INTO tenants (id, alias, status) VALUES ('acme-one', 'first', 1)",
			second:   tenant.Tenant{ID: "second", Alias: "acme-one", Status: tenant.StatusActive},
			sentinel: tenant.ErrAliasConflict,
		},
		{
			name:     "an identifier that is another tenant's alias",
			first:    "INSERT INTO tenants (id, alias, status) VALUES ('first', 'second-alias', 1)",
			second:   tenant.Tenant{ID: "second-alias", Alias: "third-alias", Status: tenant.StatusActive},
			sentinel: tenant.ErrTenantExists,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := rosterOver(t, tc.first)
			err := r.Put(context.Background(), tc.second)
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("the insert gave %v, want %v", err, tc.sentinel)
			}
			if got := countRows(t, r); got != 1 {
				t.Errorf("the refused insert left %d rows, want the 1 it started with", got)
			}
		})
	}
}

// TestPutRefusesAnAliasAnotherTenantHolds is the uniqueness the snapshot's alias
// index cannot supply: the index is built by assignment, so two rows sharing an
// alias would leave the roster quietly holding one of them.
//
// The check sits inside the write's transaction so that the refusal can name
// the tenant holding the name. The column's unique index reports the same
// collision, but as a constraint violation, which is why the check is
// load-bearing rather than redundant.
func TestPutRefusesAnAliasAnotherTenantHolds(t *testing.T) {
	r := rosterOver(t, insert("globex", "shared"))
	err := r.Put(context.Background(), tenant.Tenant{
		ID:     "acme",
		Alias:  "shared",
		Status: tenant.StatusActive,
	})
	if err == nil {
		t.Fatal("the write put one alias on two tenants")
	}
	if !errors.Is(err, tenant.ErrAliasConflict) {
		t.Fatalf("the failure is %v, want it to carry ErrAliasConflict", err)
	}
	if text := err.Error(); !strings.Contains(text, "globex") {
		t.Errorf("the failure does not name the tenant holding the alias: %v", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused write left %d rows in the table, want the 1 it started with", got)
	}
	if held, ok := rowOf(t, r, "acme"); ok {
		t.Errorf("the refused write registered the tenant anyway: %+v", held)
	}
	if held, ok := rowOf(t, r, "globex"); !ok || held.Alias != "shared" {
		t.Errorf("the refused write disturbed the row holding the alias: %+v (found=%v)", held, ok)
	}
}

// TestSetStatusRefusesAnIdentifierOutsideTheDomain is the state change's own
// predicate, judged before it looks anything up. The field is named rather than
// the value quoted back: the domain keeps the value out of error text, and an
// identifier that reaches the message is one the predicate did not judge, since
// the failure for an identifier no row carries is the one that quotes it.
func TestSetStatusRefusesAnIdentifierOutsideTheDomain(t *testing.T) {
	for _, tc := range []struct{ name, id string }{
		{"a blank", "acme corp"},
		{"a control character", "acme\ncorp"},
		{"nothing at all", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rosterOver(t, insert("acme", "acme"))
			err := r.SetStatus(context.Background(), tenant.ID(tc.id), tenant.StatusDisabled)
			if err == nil {
				t.Fatal("the state change accepted an identifier this module will not act on")
			}
			if !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("the failure is %v, want it to carry ErrInvalidTenant", err)
			}
			if text := err.Error(); !strings.Contains(text, "identifier") {
				t.Errorf("the failure does not name the field: %v", err)
			}
			if text := err.Error(); strings.Contains(text, strconv.Quote(tc.id)) {
				t.Errorf("the failure quotes the value back, so the predicate did not judge it: %v", err)
			}
		})
	}
}

// TestSetStatusRefusesAnIdentifierNoTenantCarries is the state change on a
// tenant that is not there. The identifier is named rather than the update's
// affected-row count read: a driver is free to report zero rows for an update
// that set a value to what it already was, and this call would then answer a
// tenant that exists as one that does not.
//
// What a case can hold is the refusal itself. Whether the check is the read or
// the update's count is not observable through this harness: the SQLite driver
// counts a row the update matched even when the value is unchanged, so the two
// agree on every input a case can build here.
func TestSetStatusRefusesAnIdentifierNoTenantCarries(t *testing.T) {
	r := rosterOver(t, insert("acme", "acme"))
	err := r.SetStatus(context.Background(), tenant.ID("globex"), tenant.StatusDisabled)
	if err == nil {
		t.Fatal("the state change reported success for an identifier no tenant carries")
	}
	if !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("the failure is %v, want it to carry ErrInvalidTenant", err)
	}
	if text := err.Error(); !strings.Contains(text, "globex") {
		t.Errorf("the failure does not name the identifier: %v", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused state change left %d rows, want the 1 it started with", got)
	}
}

// TestSetStatusDisabledIsServedToTheWriter is why the write rereads the table:
// disabling is an operator's action, and the process that made it must not keep
// serving a tenant it has just disabled until a refresh period happens to pass.
// A disabled tenant that still resolves as active is the defect — serving it is
// what the state is for.
func TestSetStatusDisabledIsServedToTheWriter(t *testing.T) {
	r := rosterOver(t, insert("acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("loading the roster: %v", err)
	}
	if before, ok, err := r.Get(ctx, "acme"); err != nil || !ok || before.Status != tenant.StatusActive {
		t.Fatalf("the tenant does not start out served: %+v (found=%v, err=%v)", before, ok, err)
	}

	if err := r.SetStatus(ctx, "acme", tenant.StatusDisabled); err != nil {
		t.Fatalf("disabling the tenant: %v", err)
	}

	after, ok, err := r.Get(ctx, "acme")
	if err != nil {
		t.Fatalf("reading the tenant back: %v", err)
	}
	if !ok {
		t.Fatal("the tenant left the roster instead of being disabled")
	}
	if after.Status != tenant.StatusDisabled {
		t.Errorf("the writer is still served the tenant as %s, want it disabled", after.Status)
	}
	byAlias, ok, err := r.ByAlias(ctx, "acme")
	if err != nil || !ok || byAlias.Status != tenant.StatusDisabled {
		t.Errorf("the alias still resolves to a served tenant: %+v (found=%v, err=%v)", byAlias, ok, err)
	}
}

// TestPutAcceptsATenantUnderTheAliasItAlreadyHolds is Put's exemption for the
// row that already carries the alias: the conflict check is for an alias held
// by another tenant, so writing a tenant under the alias it already carries is
// not a collision.
//
// That is the shape a seed run a second time against an existing database takes
// — the reference host's seed writes every tenant with its identifier as its
// alias — and a write that only changes a status reaches the same check. Under
// a check that treated any holder as a conflict, both would come back
// ErrAliasConflict and an operator could neither re-apply a seed nor flip a
// status without clearing the table first.
//
// The alias is resolved back through the roster and not only read from the
// table: what the write is for is the process serving its result.
func TestPutAcceptsATenantUnderTheAliasItAlreadyHolds(t *testing.T) {
	r := rosterOver(t)
	ctx := context.Background()
	if err := r.Put(ctx, tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("the first write: %v", err)
	}

	// The second write carries the same identifier and the same alias, changing
	// only the status, which is the smallest form the exemption has to admit.
	if err := r.Put(ctx, tenant.Tenant{ID: "acme", Alias: "acme", Status: tenant.StatusDisabled}); err != nil {
		t.Fatalf("re-registering the tenant under the alias it already carries: %v", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("re-registering the tenant left %d rows, want the 1 the first write made", got)
	}
	served, ok, err := r.ByAlias(ctx, "acme")
	if err != nil || !ok {
		t.Fatalf("the alias no longer resolves to the tenant that carries it: found=%v err=%v", ok, err)
	}
	if served.ID != "acme" {
		t.Errorf("the alias resolves to tenant %q, want the %q it was written under", served.ID, "acme")
	}
	if served.Status != tenant.StatusDisabled {
		t.Errorf("the alias resolves to the tenant as %s, want the second write's %s",
			served.Status, tenant.StatusDisabled)
	}
}

// TestPutReportsAWriteThatCouldNotBeReread is the failure afterWrite exists to
// report rather than swallow. When the reread that serves the writer its own
// write refuses the table — here because the table already holds a row the
// read will not act on — the write is still durable and the process is serving
// a roster that omits it. Only the caller can act on that, so Put says the
// write was applied instead of letting the refusal stand in for a write that
// never happened.
//
// The message is a claim about the table, so the case checks the table: the
// written row is read back through the handle, which is what the reread failed
// to fold into the snapshot the process serves.
func TestPutReportsAWriteThatCouldNotBeReread(t *testing.T) {
	r := rosterOver(t, insert("acme corp", "acme"))
	err := r.Put(context.Background(), tenant.Tenant{
		ID:     "globex",
		Alias:  "globex",
		Status: tenant.StatusActive,
	})
	if err == nil {
		t.Fatal("the write reported success although the roster could not be reread")
	}
	if !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("the failure is %v, want it to carry the sentinel of the read that refused the table", err)
	}
	if text := err.Error(); !strings.Contains(text, "the write was applied") {
		t.Errorf("the failure does not say the write was applied, so a caller cannot tell a refused "+
			"write from one that landed: %v", err)
	}
	if _, ok := rowOf(t, r, "globex"); !ok {
		t.Error("the failure says the write was applied, but the table does not hold the row it wrote")
	}
}

// TestSetStatusReportsAWriteThatCouldNotBeReread is the same failure on the
// other writer. The table holds a row the read will not act on — a row that
// arrived by some route other than Put — so the reread that serves the writer
// its own write refuses the whole table: the state change is durable and the
// process is left serving a roster that still holds the tenant as it was.
//
// Only the caller can act on that, so the failure says the change was applied
// instead of letting the refusal stand in for one that never happened. What the
// case reads is that conclusion — "the write was applied" — and not the
// sentence it is wrapped in, the same way the sibling case over Put reads it: a
// caller acts on the conclusion, and the wording may change under it. The
// message is a claim about the table, so the case checks the table too: the row
// is read back through the handle, which is what the reread failed to fold into
// the snapshot the process serves.
func TestSetStatusReportsAWriteThatCouldNotBeReread(t *testing.T) {
	r := rosterOver(t, insert("acme corp", "acme"), insert("globex", "globex"))
	err := r.SetStatus(context.Background(), "globex", tenant.StatusDisabled)
	if err == nil {
		t.Fatal("the state change reported success although the roster could not be reread")
	}
	if !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("the failure is %v, want it to carry the sentinel of the read that refused the table", err)
	}
	if text := err.Error(); !strings.Contains(text, "the write was applied") {
		t.Errorf("the failure does not say the write was applied, so a caller cannot tell a refused "+
			"state change from one that landed: %v", err)
	}
	held, ok := rowOf(t, r, "globex")
	if !ok {
		t.Fatal("the table does not hold the tenant whose status the change was for")
	}
	if held.Status != int(tenant.StatusDisabled) {
		t.Errorf("the row's status is %d, want the %d the change wrote — which is what the failure "+
			"claims was applied", held.Status, int(tenant.StatusDisabled))
	}
}

// TestPutTellsAnEmptyFieldApartFromABlankOne holds the two domain failures
// apart. An empty value and one carrying a blank or a control character share
// the sentinel, but their fixes differ — supply a value at all, versus correct
// the characters in one — so checkField judges the empty case first and the
// message says which one the field carried.
//
// Both fields are judged, because the empty branch runs for either: an alias
// is as able to arrive empty as an identifier.
func TestPutTellsAnEmptyFieldApartFromABlankOne(t *testing.T) {
	for _, tc := range []struct {
		name  string
		id    string
		alias string
		field string
	}{
		{"an empty identifier", "", "acme", "identifier"},
		{"an empty alias", "acme", "", "alias"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rosterOver(t, insert("globex", "globex"))
			err := r.Put(context.Background(), tenant.Tenant{
				ID:     tenant.ID(tc.id),
				Alias:  tc.alias,
				Status: tenant.StatusActive,
			})
			if err == nil {
				t.Fatal("the write accepted a tenant with an empty field")
			}
			if !errors.Is(err, tenant.ErrInvalidTenant) {
				t.Fatalf("the failure is %v, want it to carry ErrInvalidTenant", err)
			}
			text := err.Error()
			if !strings.Contains(text, "the "+tc.field+" is empty") {
				t.Errorf("the failure for an empty %s does not say the field is empty: %v", tc.field, err)
			}
			if strings.Contains(text, "carries a blank or a control character") {
				t.Errorf("the failure for an empty %s carries the sentence the blank failure carries "+
					"instead: %v", tc.field, err)
			}
		})
	}
}

// TestTheTableRefusesTwoTenantsSharingOneAlias holds the half of the alias
// invariant that the table carries rather than the writing code.
//
// Uniqueness has to hold in two places: Put reports a collision as a failure
// naming the tenant that holds the name, and the column refuses the row. The
// case that covers a collision goes through Put, so the column's half is left
// unheld by it — and losing that half is quiet. A row written by any other
// route (a statement run by hand, an import, a database this module shares) is
// then accepted, the snapshot indexes aliases by assignment, and the alias
// resolves to whichever of the two tenants the index kept while the other
// leaves the roster with nothing reporting it.
//
// The table is built from the sqlite migration set, the one the handle this
// fixture opens speaks. The postgres set is applied against PostgreSQL by the
// integration cases, which build their roster through applyMigrations too; the
// two dialects no longer ship the same text, so one dialect's statements cannot
// be run through the other's engine.
func TestTheTableRefusesTwoTenantsSharingOneAlias(t *testing.T) {
	r := rosterOverDialect(t, "sqlite", insert("acme", "acme"))

	if err := r.handle.Exec(insert("globex", "acme")).Error; err == nil {
		t.Fatal("the table took a second tenant under an alias it already holds")
	}
}
