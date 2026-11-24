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

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/dbtest"
	"github.com/vislake/speed/pkg/tenant"
)

type databaseOver struct{ handle *gorm.DB }

func (d databaseOver) DB() *gorm.DB             { return d.handle }
func (d databaseOver) Dialect() db.Dialect      { return db.SQLite }
func (d databaseOver) BlindIndex(string) string { return "" }
func (d databaseOver) Open(context.Context, string) (*gorm.DB, error) {
	return nil, errors.New("this case opens no second connection")
}

func rosterOver(t *testing.T, rows ...string) *roster {
	t.Helper()
	return rosterOverDialect(t, "sqlite", rows...)
}

func openHandle(t *testing.T) *gorm.DB {
	t.Helper()
	return openHandleAt(t, filepath.Join(t.TempDir(), "roster.db"))
}

func openHandleAt(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	return quiet(dbtest.SQLite(t, dsn, nil).DB())
}

func quiet(handle *gorm.DB) *gorm.DB {
	return handle.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}

func rosterOverDialect(t *testing.T, dialect string, rows ...string) *roster {
	t.Helper()
	return rosterOn(t, openHandle(t), dialect, rows...)
}

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

func insert(id, name string) string {
	return "INSERT INTO tenants (id, name, status) VALUES ('" + id + "', '" + name + "', 1)"
}

func TestReadRefusesARowOutsideTheIdentifierDomain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		row   string
		field string
	}{
		{"a blank inside the identifier", insert("acme corp", "acme"), "identifier"},
		{"a control character in the identifier", insert(`acme`+"\n"+`corp`, "acme"), "identifier"},
		{"an empty identifier", insert("", "acme"), "identifier"},
		{"a blank inside the name", insert("acme", "ac me"), "name"},
		{"a control character in the name", insert("acme", `ac`+"\t"+`me`), "name"},
		{"an empty name", insert("acme", ""), "name"},
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
			if text := err.Error(); strings.ContainsAny(text, "\n\t\x00") {
				t.Errorf("the failure carries a control character of its own: %q", text)
			}
			if r.snap.Load() != nil {
				t.Error("the read published a snapshot although it refused the table")
			}
		})
	}
}

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
	if _, ok := s.byName["acme"]; !ok {
		t.Error("the snapshot does not hold the tenant the table carries")
	}
	if _, ok := s.byName["globex"]; !ok {
		t.Error("the snapshot does not hold the tenant with a long identifier, which this module " +
			"has no reason to refuse")
	}
}

func TestReadLeavesThePreviousSnapshotInPlaceWhenItRefuses(t *testing.T) {
	r := rosterOver(t, insert("acme", "acme"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("the first read: %v", err)
	}
	before, err := r.current()
	if err != nil {
		t.Fatalf("the first read published nothing: %v", err)
	}

	if err = r.handle.Exec(`UPDATE tenants SET id = 'acme corp' WHERE name = 'acme'`).Error; err != nil {
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
	if _, ok := after.byName["acme"]; !ok {
		t.Error("the previous snapshot no longer answers for the tenant it held")
	}
}

func rosterWithoutTable(t *testing.T) *roster {
	t.Helper()
	return newRoster(settings{}, databaseOver{handle: openHandle(t)})
}

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

func TestLoadEndsTheStartupWhenTheTableIsNotThere(t *testing.T) {
	t.Run("never created", func(t *testing.T) {
		requireRosterMissingOnLoad(t, rosterWithoutTable(t))
	})
	t.Run("dropped after a load", func(t *testing.T) {
		r := rosterOver(t, insert("acme", "acme"))
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
	if errors.Is(err, tenant.ErrInvalidTenant) {
		t.Errorf("the failure %s returns carries ErrInvalidTenant, which a caller reads as a tenant "+
			"that is not there: %v", method, err)
	}
}

func TestAnUnreadRosterRefusesTheReads(t *testing.T) {
	r := rosterOver(t)
	ctx := context.Background()

	got, found, err := r.Get(ctx, "acme")
	requireUnreadRefusal(t, "Get", got, found, err)

	byName, found, err := r.ByName(ctx, "acme")
	requireUnreadRefusal(t, "ByName", byName, found, err)

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

func namesOf(list []tenant.Tenant) string {
	names := make([]string, len(list))
	for i, entry := range list {
		names[i] = entry.Name
	}
	return strings.Join(names, ",")
}

func TestListIsOrderedByNameAndIsACopy(t *testing.T) {
	r := rosterOver(t,
		insert("t_3_carol", "alice"),
		insert("t_1_alice", "carol"),
		insert("t_2_bob", "bob"),
	)
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading a conforming roster: %v", err)
	}

	got, err := r.List(ctx)
	if err != nil {
		t.Fatalf("listing the roster: %v", err)
	}
	if held := namesOf(got); held != "alice,bob,carol" {
		t.Errorf("List returned the names %q, want them ordered by name %q",
			held, "alice,bob,carol")
	}

	if len(got) > 0 {
		got[0] = tenant.Tenant{ID: "zzz", Name: "zzz", Status: tenant.StatusActive}
	}
	again, err := r.List(ctx)
	if err != nil {
		t.Fatalf("listing the roster a second time: %v", err)
	}
	if held := namesOf(again); held != "alice,bob,carol" {
		t.Errorf("a caller's change to the slice it was handed changed the next list: %q, want %q",
			held, "alice,bob,carol")
	}
}

func TestNameOfAnswersTheNameByIdentifier(t *testing.T) {
	r := rosterOver(t, insert("t_alice", "alice"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading a conforming roster: %v", err)
	}
	name, err := r.NameOf(ctx, "t_alice")
	if err != nil {
		t.Fatalf("reading the name: %v", err)
	}
	if name != "alice" {
		t.Errorf("name = %q, want alice", name)
	}
	missing, err := r.NameOf(ctx, "t_ghost")
	if err != nil {
		t.Fatalf("reading an unknown identifier's name: %v", err)
	}
	if missing != "" {
		t.Errorf("an unknown identifier answered %q, want the empty name", missing)
	}
}

func TestTenantIDOfAnswersTheIdentifierByName(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading a conforming roster: %v", err)
	}
	id, found, err := r.TenantIDOf(ctx, "acme")
	if err != nil {
		t.Fatalf("reading the identifier: %v", err)
	}
	if !found || id != "t_acme" {
		t.Errorf("TenantIDOf(acme) = %q, %v, want t_acme, true", id, found)
	}
	if id, found, err := r.TenantIDOf(ctx, "ghost"); err != nil || found || id != "" {
		t.Errorf("TenantIDOf(ghost) = %q, %v, %v, want no identifier, false and no error", id, found, err)
	}
}

func TestTenantDescriptionOfAnswersTheNameTheDisplayNameAndTheSystemFlag(t *testing.T) {
	r := rosterOver(t,
		"INSERT INTO tenants (id, name, status, display_name) VALUES ('t_acme', 'acme', 1, 'Acme Corporation')",
		"INSERT INTO tenants (id, name, status) VALUES ('system', 'system', 1)",
	)
	ctx := context.Background()
	if err := r.read(ctx); err != nil {
		t.Fatalf("reading a conforming roster: %v", err)
	}
	got, err := r.TenantDescriptionOf(ctx, "t_acme")
	if err != nil {
		t.Fatalf("reading the description: %v", err)
	}
	if got != (authn.TenantDescription{Name: "acme", DisplayName: "Acme Corporation"}) {
		t.Errorf("TenantDescriptionOf(t_acme) = %+v, want acme and Acme Corporation, not system", got)
	}
	system, err := r.TenantDescriptionOf(ctx, "system")
	if err != nil {
		t.Fatalf("reading the system tenant's description: %v", err)
	}
	if !system.System || system.Name != "system" {
		t.Errorf("TenantDescriptionOf(system) = %+v, want the system flag and the name system", system)
	}
	if missing, err := r.TenantDescriptionOf(ctx, "t_ghost"); err != nil || missing != (authn.TenantDescription{}) {
		t.Errorf("TenantDescriptionOf(t_ghost) = %+v, %v, want the zero description and no error", missing, err)
	}
}

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
		{"by name", func() (tenant.Tenant, bool, error) { return r.ByName(ctx, "absent") }},
	} {
		t.Run(lookup.name, func(t *testing.T) {
			found, ok, err := lookup.call()
			if err != nil {
				t.Fatalf("the lookup failed for a name no tenant carries: %v", err)
			}
			if ok {
				t.Error("the lookup reported a tenant for a name no row carries")
			}
			if found.ID != "" || found.Name != "" {
				t.Errorf("the lookup returned %+v for a name no row carries, want the zero tenant", found)
			}
		})
	}
}

func countRows(t *testing.T, r *roster) int64 {
	t.Helper()
	var n int64
	if err := r.handle.Model(&row{}).Count(&n).Error; err != nil {
		t.Fatalf("counting the roster's rows: %v", err)
	}
	return n
}

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

func TestPutRefusesATenantOutsideTheIdentifierDomain(t *testing.T) {
	for _, tc := range []struct {
		label string
		id    string
		name  string
		field string
	}{
		{"a blank inside the identifier", "acme corp", "acme", "identifier"},
		{"a control character in the identifier", "acme\ncorp", "acme", "identifier"},
		{"an empty identifier", "", "acme", "identifier"},
		{"a blank inside the name", "acme", "ac me", "name"},
		{"a control character in the name", "acme", "ac\tme", "name"},
		{"an empty name", "acme", "", "name"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			r := rosterOver(t, insert("globex", "globex"))
			err := r.Put(context.Background(), tenant.Tenant{
				ID:     tenant.ID(tc.id),
				Name:   tc.name,
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
			if text := err.Error(); strings.ContainsAny(text, "\n\t\x00") {
				t.Errorf("the failure carries a control character of its own: %q", text)
			}
			if got := countRows(t, r); got != 1 {
				t.Errorf("the refused write left %d rows in the table, want the 1 it started with", got)
			}
		})
	}
}

func TestPutRewritesByIdentifier(t *testing.T) {
	r := rosterOver(t)
	ctx := context.Background()

	if err := r.Put(ctx, tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("registering a tenant: %v", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Fatalf("the first write left %d rows, want 1", got)
	}

	if err := r.Put(ctx, tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusDisabled}); err != nil {
		t.Fatalf("rewriting the tenant: %v", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the second write left %d rows, want the 1 the first wrote", got)
	}
	held, ok := rowOf(t, r, "acme")
	if !ok {
		t.Fatal("the identifier the row was written under is no longer in the table")
	}
	if held.Name != "acme" {
		t.Errorf("the row's name is %q, want the %q it was written with", held.Name, "acme")
	}
	if held.Status != int(tenant.StatusDisabled) {
		t.Errorf("the row's status is %d, want the second write's %d", held.Status, int(tenant.StatusDisabled))
	}
	if written, ok, err := r.ByName(ctx, "acme"); err != nil || !ok || written.Status != tenant.StatusDisabled {
		t.Errorf("the roster does not resolve the name the row was written with: %+v (found=%v, err=%v)",
			written, ok, err)
	}
}

func TestPutRefusesToChangeAName(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	err := r.Put(context.Background(), tenant.Tenant{
		ID: "t_acme", Name: "renamed", Status: tenant.StatusDisabled,
	})
	if !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Fatalf("rewriting a tenant under another name gave %v, want ErrInvalidTenant", err)
	}
	requireRow(t, r, "t_acme", row{ID: "t_acme", Name: "acme", Status: int(tenant.StatusActive)})
}

func TestCreateRefusesANameThatIsAnotherTenantsIdentifier(t *testing.T) {
	r := rosterOver(t, insert("acme-one", "first"))
	err := r.Create(context.Background(), tenant.Tenant{
		ID: "second", Name: "acme-one", Status: tenant.StatusActive,
	})
	if !errors.Is(err, tenant.ErrNameConflict) {
		t.Fatalf("creating a tenant whose name is another tenant's identifier gave %v, want ErrNameConflict", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused creation left %d rows, want the 1 it started with", got)
	}
}

func TestCreateRefusesAnIdentifierThatIsAnotherTenantsName(t *testing.T) {
	r := rosterOver(t, insert("first", "second-name"))
	err := r.Create(context.Background(), tenant.Tenant{
		ID: "second-name", Name: "third-name", Status: tenant.StatusActive,
	})
	if !errors.Is(err, tenant.ErrTenantExists) {
		t.Fatalf("creating a tenant whose identifier is another tenant's name gave %v, want ErrTenantExists", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused creation left %d rows, want the 1 it started with", got)
	}
}

func TestPutRefusesACrossNamespaceInsert(t *testing.T) {
	cases := []struct {
		name     string
		first    string
		second   tenant.Tenant
		sentinel error
	}{
		{
			name:     "a name that is another tenant's identifier",
			first:    "INSERT INTO tenants (id, name, status) VALUES ('acme-one', 'first', 1)",
			second:   tenant.Tenant{ID: "second", Name: "acme-one", Status: tenant.StatusActive},
			sentinel: tenant.ErrNameConflict,
		},
		{
			name:     "an identifier that is another tenant's name",
			first:    "INSERT INTO tenants (id, name, status) VALUES ('first', 'second-name', 1)",
			second:   tenant.Tenant{ID: "second-name", Name: "third-name", Status: tenant.StatusActive},
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

func TestPutRefusesANameAnotherTenantHolds(t *testing.T) {
	r := rosterOver(t, insert("globex", "shared"))
	err := r.Put(context.Background(), tenant.Tenant{
		ID:     "acme",
		Name:   "shared",
		Status: tenant.StatusActive,
	})
	if err == nil {
		t.Fatal("the write put one name on two tenants")
	}
	if !errors.Is(err, tenant.ErrNameConflict) {
		t.Fatalf("the failure is %v, want it to carry ErrNameConflict", err)
	}
	if text := err.Error(); !strings.Contains(text, "globex") {
		t.Errorf("the failure does not name the tenant holding the name: %v", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("the refused write left %d rows in the table, want the 1 it started with", got)
	}
	if held, ok := rowOf(t, r, "acme"); ok {
		t.Errorf("the refused write registered the tenant anyway: %+v", held)
	}
	if held, ok := rowOf(t, r, "globex"); !ok || held.Name != "shared" {
		t.Errorf("the refused write disturbed the row holding the name: %+v (found=%v)", held, ok)
	}
}

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
	byName, ok, err := r.ByName(ctx, "acme")
	if err != nil || !ok || byName.Status != tenant.StatusDisabled {
		t.Errorf("the name still resolves to a served tenant: %+v (found=%v, err=%v)", byName, ok, err)
	}
}

func TestPutAcceptsATenantUnderTheNameItAlreadyHolds(t *testing.T) {
	r := rosterOver(t)
	ctx := context.Background()
	if err := r.Put(ctx, tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusActive}); err != nil {
		t.Fatalf("the first write: %v", err)
	}

	if err := r.Put(ctx, tenant.Tenant{ID: "acme", Name: "acme", Status: tenant.StatusDisabled}); err != nil {
		t.Fatalf("re-registering the tenant under the name it already carries: %v", err)
	}
	if got := countRows(t, r); got != 1 {
		t.Errorf("re-registering the tenant left %d rows, want the 1 the first write made", got)
	}
	served, ok, err := r.ByName(ctx, "acme")
	if err != nil || !ok {
		t.Fatalf("the name no longer resolves to the tenant that carries it: found=%v err=%v", ok, err)
	}
	if served.ID != "acme" {
		t.Errorf("the name resolves to tenant %q, want the %q it was written under", served.ID, "acme")
	}
	if served.Status != tenant.StatusDisabled {
		t.Errorf("the name resolves to the tenant as %s, want the second write's %s",
			served.Status, tenant.StatusDisabled)
	}
}

func TestPutReportsAWriteThatCouldNotBeReread(t *testing.T) {
	r := rosterOver(t, insert("acme corp", "acme"))
	err := r.Put(context.Background(), tenant.Tenant{
		ID:     "globex",
		Name:   "globex",
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

func TestPutTellsAnEmptyFieldApartFromABlankOne(t *testing.T) {
	for _, tc := range []struct {
		label string
		id    string
		name  string
		field string
	}{
		{"an empty identifier", "", "acme", "identifier"},
		{"an empty name", "acme", "", "name"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			r := rosterOver(t, insert("globex", "globex"))
			err := r.Put(context.Background(), tenant.Tenant{
				ID:     tenant.ID(tc.id),
				Name:   tc.name,
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

func TestTheTableRefusesTwoTenantsSharingOneName(t *testing.T) {
	r := rosterOverDialect(t, "sqlite", insert("acme", "acme"))

	if err := r.handle.Exec(insert("globex", "acme")).Error; err == nil {
		t.Fatal("the table took a second tenant under a name it already holds")
	}
}
