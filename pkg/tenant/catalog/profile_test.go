package catalog

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/vislake/speed/pkg/tenant"
)

// The cases in this file are about the organization profile and the creation
// time: the columns the migration adds, the write paths that carry them, and
// the read that refuses a row holding a profile this module will not serve.

// profileColumn is one column the profile migration declares, with the shape
// the migration owes it: whether it is NOT NULL, and the default a row that
// predates it takes.
type profileColumn struct {
	name    string
	notNull bool
	def     string
}

var profileColumns = []profileColumn{
	{name: "display_name", notNull: true, def: "''"},
	{name: "description", notNull: true, def: "''"},
	{name: "contact_email", notNull: true, def: "''"},
	{name: "website", notNull: true, def: "''"},
	{name: "created_at", notNull: false},
}

// TestTheMigrationsDeclareTheOrganizationProfileForBothDialects holds each
// dialect's migration set to the five columns and their defaults. The DDL is
// applied to a SQLite table because these columns use no dialect-specific
// construct beyond the nullable time type, so both files can be exercised
// without the compose stack.
func TestTheMigrationsDeclareTheOrganizationProfileForBothDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			handle := openHandle(t)
			// The base table is built from the SQLite set's first two files,
			// whose statements SQLite accepts; the file under test is the
			// profile migration of the dialect named, executed as it ships.
			for _, name := range []string{"0001_tenants.sql", "0002_tenants_disabled_by.sql"} {
				ddl, err := fs.ReadFile(migrationsFS, "sqlite/"+name)
				if err != nil {
					t.Fatalf("reading sqlite/%s: %v", name, err)
				}
				if err := handle.Exec(string(ddl)).Error; err != nil {
					t.Fatalf("applying sqlite/%s: %v", name, err)
				}
			}
			ddl, err := fs.ReadFile(migrationsFS, dialect+"/0003_tenants_profile.sql")
			if err != nil {
				t.Fatalf("reading the %s profile migration: %v", dialect, err)
			}
			if err := handle.Exec(string(ddl)).Error; err != nil {
				t.Fatalf("applying the %s profile migration: %v", dialect, err)
			}

			var columns []struct {
				Name    string
				NotNull int            `gorm:"column:notnull"`
				Default sql.NullString `gorm:"column:dflt_value"`
			}
			if err := handle.Raw("PRAGMA table_info(tenants)").Scan(&columns).Error; err != nil {
				t.Fatalf("reading the columns of tenants: %v", err)
			}
			held := make(map[string]struct {
				notNull bool
				def     string
			}, len(columns))
			for _, column := range columns {
				held[column.Name] = struct {
					notNull bool
					def     string
				}{notNull: column.NotNull != 0, def: column.Default.String}
			}
			for _, want := range profileColumns {
				got, ok := held[want.name]
				if !ok {
					t.Errorf("the %s migration declares no %s column", dialect, want.name)
					continue
				}
				if got.notNull != want.notNull {
					t.Errorf("tenants.%s is NOT NULL=%v, want %v", want.name, got.notNull, want.notNull)
				}
				if got.def != want.def {
					t.Errorf("tenants.%s defaults to %q, want %q", want.name, got.def, want.def)
				}
			}

			// A row that predates the columns takes their defaults rather than
			// being rejected: the migration does not backfill and does not make
			// the profile mandatory.
			if err := handle.Exec(
				"INSERT INTO tenants (id, alias, status) VALUES ('t_old', 'old', 1)").Error; err != nil {
				t.Fatalf("writing a row without the profile columns: %v", err)
			}
			var got struct {
				DisplayName  string
				Description  string
				ContactEmail string
				Website      string
				CreatedAt    *time.Time
			}
			if err := handle.Raw(
				"SELECT display_name, description, contact_email, website, created_at " +
					"FROM tenants WHERE id = 't_old'").Scan(&got).Error; err != nil {
				t.Fatalf("reading the defaulted row: %v", err)
			}
			if got.DisplayName != "" || got.Description != "" || got.ContactEmail != "" || got.Website != "" {
				t.Errorf("an existing row reads its profile as %+v, want every field empty", got)
			}
			if got.CreatedAt != nil {
				t.Errorf("an existing row reads a creation time %v, want none", got.CreatedAt)
			}
		})
	}
}

// TestCreatePersistsTheOrganizationProfile holds the creation path to the
// profile it was given, in the table and in the writer's snapshot.
func TestCreatePersistsTheOrganizationProfile(t *testing.T) {
	r := rosterOver(t)
	profile := tenant.Profile{
		DisplayName:  "Acme Corporation",
		Description:  "Makes the things that make the other things.",
		ContactEmail: "ops@acme.test",
		Website:      "https://acme.test/about",
	}
	want := tenant.Tenant{ID: "t_acme", Alias: "acme", Status: tenant.StatusActive, Profile: profile}
	if err := r.Create(context.Background(), want); err != nil {
		t.Fatalf("creating a tenant with a profile: %v", err)
	}

	var held struct {
		DisplayName  string
		Description  string
		ContactEmail string
		Website      string
	}
	if err := r.handle.Raw(
		"SELECT display_name, description, contact_email, website FROM tenants WHERE id = 't_acme'").
		Scan(&held).Error; err != nil {
		t.Fatalf("reading the created row: %v", err)
	}
	if held.DisplayName != profile.DisplayName || held.Description != profile.Description ||
		held.ContactEmail != profile.ContactEmail || held.Website != profile.Website {
		t.Errorf("the table holds the profile as %+v, want %+v", held, profile)
	}

	got, ok, err := r.Get(context.Background(), "t_acme")
	if err != nil || !ok {
		t.Fatalf("reading the created tenant back: found=%v err=%v", ok, err)
	}
	if got.Profile != profile {
		t.Errorf("the snapshot carries the profile as %+v, want %+v", got.Profile, profile)
	}
}

// TestSetProfileWritesOnlyTheProfileColumns holds SetProfile to the four
// profile columns: the alias, the status, the disable source and the creation
// time are what other paths own, and a profile update must not write them back
// from a value it read earlier.
func TestSetProfileWritesOnlyTheProfileColumns(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	ctx := context.Background()
	if err := r.SetStatus(ctx, "t_acme", tenant.StatusDisabled); err != nil {
		t.Fatalf("disabling the tenant: %v", err)
	}
	if err := r.SetAlias(ctx, "t_acme", "acme-renamed"); err != nil {
		t.Fatalf("renaming the tenant: %v", err)
	}
	before, ok := rowOf(t, r, "t_acme")
	if !ok {
		t.Fatal("the tenant is not in the table")
	}

	profile := tenant.Profile{
		DisplayName:  "Acme Corporation",
		Description:  "A description.",
		ContactEmail: "hello@acme.test",
		Website:      "https://acme.test",
	}
	if err := r.SetProfile(ctx, "t_acme", profile); err != nil {
		t.Fatalf("writing the profile: %v", err)
	}

	after, ok := rowOf(t, r, "t_acme")
	if !ok {
		t.Fatal("the tenant left the table")
	}
	var columns struct {
		DisplayName  string
		Description  string
		ContactEmail string
		Website      string
	}
	if err := r.handle.Raw(
		"SELECT display_name, description, contact_email, website FROM tenants WHERE id = 't_acme'").
		Scan(&columns).Error; err != nil {
		t.Fatalf("reading the profile columns: %v", err)
	}
	if columns.DisplayName != profile.DisplayName || columns.Description != profile.Description ||
		columns.ContactEmail != profile.ContactEmail || columns.Website != profile.Website {
		t.Errorf("the profile columns read %+v, want %+v", columns, profile)
	}
	if after.Alias != before.Alias || after.Status != before.Status ||
		after.DisabledBy != before.DisabledBy || !sameTime(readCreatedAt(t, r, "t_acme"), nil) {
		t.Errorf("SetProfile changed more than the profile: before %+v, after %+v", before, after)
	}

	got, _, err := r.Get(ctx, "t_acme")
	if err != nil {
		t.Fatalf("reading the tenant back: %v", err)
	}
	if got.Profile != profile {
		t.Errorf("the snapshot carries the profile as %+v, want %+v", got.Profile, profile)
	}
}

// TestSetProfileRefusesWhatItCannotWrite holds the edges: an invalid profile,
// an identifier no row carries, and a tombstone.
func TestSetProfileRefusesWhatItCannotWrite(t *testing.T) {
	r := rosterOver(t, insert("t_active", "active"),
		insertRow("t_deleted", "deleted", tenant.StatusDeleted, tenant.DisabledByOperator))
	ctx := context.Background()

	if err := r.SetProfile(ctx, "t_acme", tenant.Profile{}); !errors.Is(err, tenant.ErrInvalidTenant) {
		t.Errorf("writing the profile of a tenant that is not there gave %v, want ErrInvalidTenant", err)
	}
	if err := r.SetProfile(ctx, "t_deleted", tenant.Profile{}); !errors.Is(err, tenant.ErrTenantDeleted) {
		t.Errorf("writing the profile of a tombstone gave %v, want ErrTenantDeleted", err)
	}
	bad := tenant.Profile{DisplayName: strings.Repeat("a", 101)}
	if err := r.SetProfile(ctx, "t_active", bad); !errors.Is(err, tenant.ErrInvalidProfile) {
		t.Errorf("writing an invalid profile gave %v, want ErrInvalidProfile", err)
	}
}

// TestTheSnapshotCarriesTheOrganizationProfile holds the whole-table read to
// the profile columns: a row written by another route reaches the snapshot with
// what the table holds.
func TestTheSnapshotCarriesTheOrganizationProfile(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants "+
		"(id, alias, status, display_name, description, contact_email, website) VALUES "+
		"('t_acme', 'acme', 1, 'Acme Corporation', 'A description.', 'ops@acme.test', 'https://acme.test')")
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading the roster: %v", err)
	}
	want := tenant.Profile{
		DisplayName:  "Acme Corporation",
		Description:  "A description.",
		ContactEmail: "ops@acme.test",
		Website:      "https://acme.test",
	}
	got, ok, err := r.Get(context.Background(), "t_acme")
	if err != nil || !ok {
		t.Fatalf("reading the tenant: found=%v err=%v", ok, err)
	}
	if got.Profile != want {
		t.Errorf("the snapshot carries the profile as %+v, want %+v", got.Profile, want)
	}
	list, err := r.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("listing the roster: %v, %d rows", err, len(list))
	}
	if list[0].Profile != want {
		t.Errorf("the listed profile is %+v, want %+v", list[0].Profile, want)
	}
}

// TestReadRefusesAnInvalidStoredProfile holds the read to the profile's domain:
// a table can be written without Create or SetProfile, and what such a row
// carries is handed to every caller that resolves a tenant.
func TestReadRefusesAnInvalidStoredProfile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile string
	}{
		{"a display name beyond its bound", "'" + strings.Repeat("a", 101) + "', '', '', ''"},
		{"a description beyond its bound", "'', '" + strings.Repeat("a", 501) + "', '', ''"},
		{"a contact email that is not one address", "'', '', 'not an address', ''"},
		{"a website that is not absolute", "'', '', '', '/relative'"},
		{"a website with userinfo", "'', '', '', 'https://user@acme.test'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rosterOver(t, "INSERT INTO tenants "+
				"(id, alias, status, display_name, description, contact_email, website) VALUES "+
				"('t_acme', 'acme', 1, "+tc.profile+")")
			err := r.read(context.Background())
			if !errors.Is(err, tenant.ErrInvalidProfile) {
				t.Fatalf("reading a row with an invalid profile gave %v, want ErrInvalidProfile", err)
			}
		})
	}
}

// TestCreatedAtIsNullForAnExistingRowAndSetForANewOne holds the nullable time:
// a row that predates the column keeps no time, and a creation writes the
// moment it happened in UTC.
func TestCreatedAtIsNullForAnExistingRowAndSetForANewOne(t *testing.T) {
	r := rosterOver(t, insert("t_old", "old"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	old, ok, err := r.Get(context.Background(), "t_old")
	if err != nil || !ok {
		t.Fatalf("reading the existing tenant: found=%v err=%v", ok, err)
	}
	if old.CreatedAt != nil {
		t.Errorf("an existing row reads a creation time %v, want none", old.CreatedAt)
	}

	before := time.Now().UTC().Add(-time.Minute)
	if err = r.Create(context.Background(), tenant.Tenant{
		ID: "t_new", Alias: "new", Status: tenant.StatusActive,
	}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	after := time.Now().UTC().Add(time.Minute)

	created, ok, err := r.Get(context.Background(), "t_new")
	if err != nil || !ok {
		t.Fatalf("reading the created tenant: found=%v err=%v", ok, err)
	}
	if created.CreatedAt == nil {
		t.Fatal("a created row reads no creation time, want the moment of creation")
	}
	if created.CreatedAt.Before(before) || created.CreatedAt.After(after) {
		t.Errorf("the creation time is %v, want between %v and %v", created.CreatedAt, before, after)
	}
	if _, offset := created.CreatedAt.Zone(); offset != 0 {
		t.Errorf("the creation time is %v, want UTC", created.CreatedAt)
	}
}

// sameTime reports whether two nullable times name the same instant, with both
// nil counting as the same one.
func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// readCreatedAt reads one row's creation time straight from the table.
func readCreatedAt(t *testing.T, r *roster, id string) *time.Time {
	t.Helper()
	var held []struct{ CreatedAt *time.Time }
	if err := r.handle.Raw("SELECT created_at FROM tenants WHERE id = ?", id).Scan(&held).Error; err != nil {
		t.Fatalf("reading the creation time of %q: %v", id, err)
	}
	if len(held) != 1 {
		t.Fatalf("the table holds %d rows for %q, want one", len(held), id)
	}
	return held[0].CreatedAt
}

// TestPutRecordsTheCreationTimeInUTC holds the registration path's time: an
// insert through Put records the real moment in UTC, as Create does, and a
// rewrite of the same row leaves that moment alone.
//
// The case forces the handle's auto-create clock into a non-UTC zone. A value
// left to gorm would be taken from that clock and land with the offset; only an
// explicit UTC value can pass.
func TestPutRecordsTheCreationTimeInUTC(t *testing.T) {
	r := rosterOver(t, "")
	r.handle.NowFunc = func() time.Time {
		return time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC+8", 8*60*60))
	}
	if err := r.Put(context.Background(), tenant.Tenant{
		ID: "t_put", Alias: "put", Status: tenant.StatusActive,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	created := readCreatedAt(t, r, "t_put")
	if created == nil {
		t.Fatal("a row inserted through Put reads no creation time, want the moment of registration")
	}
	if _, offset := created.Zone(); offset != 0 {
		t.Errorf("the creation time is %v, want UTC", created)
	}

	if err := r.Put(context.Background(), tenant.Tenant{
		ID: "t_put", Alias: "put-renamed", Status: tenant.StatusDisabled,
	}); err != nil {
		t.Fatalf("rewriting through Put: %v", err)
	}
	rewritten := readCreatedAt(t, r, "t_put")
	if !sameTime(created, rewritten) {
		t.Errorf("the creation time became %v after a rewrite, want %v", rewritten, created)
	}
}
