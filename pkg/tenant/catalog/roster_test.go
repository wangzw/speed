package catalog

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/db"
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
// into it.
//
// The rows arrive as statements rather than through Put, which is the point:
// what these cases are about is a row that reached the table by some other
// route, and Put is the one route that would have refused it. The schema comes
// from the migration file itself, so a case cannot drift from the table the
// module really reads.
func rosterOver(t *testing.T, rows ...string) *roster {
	t.Helper()
	handle, err := gorm.Open(sqlitedriver.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening the handle: %v", err)
	}
	ddl, err := fs.ReadFile(migrationsFS, "sqlite/0001_tenants.sql")
	if err != nil {
		t.Fatalf("reading the migration: %v", err)
	}
	if err := handle.Exec(string(ddl)).Error; err != nil {
		t.Fatalf("creating the table: %v", err)
	}
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
		{"an identifier past the length bound", insert(strings.Repeat("a", maxIdentifier+1), "acme"), "identifier"},
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
func TestReadPublishesAConformingRoster(t *testing.T) {
	// The identifier of exactly the bound's length is inside it.
	r := rosterOver(t,
		insert("acme", "acme"),
		insert(strings.Repeat("a", maxIdentifier), "globex"),
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
		t.Error("the snapshot does not hold the tenant whose identifier is exactly the bound")
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
