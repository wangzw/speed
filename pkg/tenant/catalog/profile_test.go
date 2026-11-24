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
	{name: "created_at", notNull: true, def: "CURRENT_TIMESTAMP"},
}

func TestTheMigrationsDeclareTheOrganizationProfileForBothDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			handle := openHandle(t)
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

			if err := handle.Exec(
				"INSERT INTO tenants (id, name, status) VALUES ('t_old', 'old', 1)").Error; err != nil {
				t.Fatalf("writing a row without the profile columns: %v", err)
			}
			var got struct {
				DisplayName  string
				Description  string
				ContactEmail string
				Website      string
				CreatedAt    sql.NullString
			}
			if err := handle.Raw(
				"SELECT display_name, description, contact_email, website, created_at " +
					"FROM tenants WHERE id = 't_old'").Scan(&got).Error; err != nil {
				t.Fatalf("reading the defaulted row: %v", err)
			}
			if got.DisplayName != "" || got.Description != "" || got.ContactEmail != "" || got.Website != "" {
				t.Errorf("an existing row reads its profile as %+v, want every field empty", got)
			}
			if !got.CreatedAt.Valid || got.CreatedAt.String == "" {
				t.Errorf("an existing row reads no creation time, want the migration's default instant")
			}
		})
	}
}

func TestCreatePersistsTheOrganizationProfile(t *testing.T) {
	r := rosterOver(t)
	profile := tenant.Profile{
		DisplayName:  "Acme Corporation",
		Description:  "Makes the things that make the other things.",
		ContactEmail: "ops@acme.test",
		Website:      "https://acme.test/about",
	}
	want := tenant.Tenant{ID: "t_acme", Name: "acme", Status: tenant.StatusActive, Profile: profile}
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

func TestSetProfileWritesOnlyTheProfileColumns(t *testing.T) {
	r := rosterOver(t, insert("t_acme", "acme"))
	ctx := context.Background()
	if err := r.SetStatus(ctx, "t_acme", tenant.StatusDisabled); err != nil {
		t.Fatalf("disabling the tenant: %v", err)
	}
	if err := r.handle.Exec("UPDATE tenants SET name = 'acme-renamed' WHERE id = 't_acme'").Error; err != nil {
		t.Fatalf("renaming the tenant around the roster: %v", err)
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
	if after.Name != before.Name || after.Status != before.Status ||
		after.DisabledBy != before.DisabledBy || after.OwnerAccount != before.OwnerAccount ||
		!after.CreatedAt.Equal(before.CreatedAt) || !sameTime(after.DeletedAt, before.DeletedAt) {
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

func TestTheSnapshotCarriesTheOrganizationProfile(t *testing.T) {
	r := rosterOver(t, "INSERT INTO tenants "+
		"(id, name, status, display_name, description, contact_email, website) VALUES "+
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
				"(id, name, status, display_name, description, contact_email, website) VALUES "+
				"('t_acme', 'acme', 1, "+tc.profile+")")
			err := r.read(context.Background())
			if !errors.Is(err, tenant.ErrInvalidProfile) {
				t.Fatalf("reading a row with an invalid profile gave %v, want ErrInvalidProfile", err)
			}
		})
	}
}

func TestCreatedAtIsMandatoryForEveryRow(t *testing.T) {
	r := rosterOver(t, insert("t_old", "old"))
	if err := r.read(context.Background()); err != nil {
		t.Fatalf("reading: %v", err)
	}
	old, ok, err := r.Get(context.Background(), "t_old")
	if err != nil || !ok {
		t.Fatalf("reading the existing tenant: found=%v err=%v", ok, err)
	}
	if old.CreatedAt.IsZero() {
		t.Error("an existing row reads no creation time, want the migration's default instant")
	}

	before := time.Now().UTC().Add(-time.Minute)
	if err = r.Create(context.Background(), tenant.Tenant{
		ID: "t_new", Name: "new", Status: tenant.StatusActive,
	}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	after := time.Now().UTC().Add(time.Minute)

	created, ok, err := r.Get(context.Background(), "t_new")
	if err != nil || !ok {
		t.Fatalf("reading the created tenant: found=%v err=%v", ok, err)
	}
	if created.CreatedAt.Before(before) || created.CreatedAt.After(after) {
		t.Errorf("the creation time is %v, want between %v and %v", created.CreatedAt, before, after)
	}
	if _, offset := created.CreatedAt.Zone(); offset != 0 {
		t.Errorf("the creation time is %v, want UTC", created.CreatedAt)
	}
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func readCreatedAt(t *testing.T, r *roster, id string) time.Time {
	t.Helper()
	var held []struct{ CreatedAt time.Time }
	if err := r.handle.Raw("SELECT created_at FROM tenants WHERE id = ?", id).Scan(&held).Error; err != nil {
		t.Fatalf("reading the creation time of %q: %v", id, err)
	}
	if len(held) != 1 {
		t.Fatalf("the table holds %d rows for %q, want one", len(held), id)
	}
	return held[0].CreatedAt
}

func TestTheSQLiteProfileMigrationCarriesATableHoldingRows(t *testing.T) {
	handle := openHandle(t)
	for _, name := range []string{"0001_tenants.sql", "0002_tenants_disabled_by.sql"} {
		ddl, err := fs.ReadFile(migrationsFS, "sqlite/"+name)
		if err != nil {
			t.Fatalf("reading sqlite/%s: %v", name, err)
		}
		if err := handle.Exec(string(ddl)).Error; err != nil {
			t.Fatalf("applying sqlite/%s: %v", name, err)
		}
	}
	if err := handle.Exec(
		"INSERT INTO tenants (id, name, status, disabled_by) VALUES ('t_system', 'system', 1, 0)").Error; err != nil {
		t.Fatalf("writing the row the migration has to carry: %v", err)
	}

	ddl, err := fs.ReadFile(migrationsFS, "sqlite/0003_tenants_profile.sql")
	if err != nil {
		t.Fatalf("reading sqlite/0003_tenants_profile.sql: %v", err)
	}
	if err := handle.Exec(string(ddl)).Error; err != nil {
		t.Fatalf("applying the SQLite profile migration over a table holding a row: %v", err)
	}

	var carried struct {
		ID        string
		Name      string
		Status    int
		CreatedAt sql.NullString
	}
	if err := handle.Raw(
		"SELECT id, name, status, created_at FROM tenants WHERE id = 't_system'").Scan(&carried).Error; err != nil {
		t.Fatalf("reading the carried row: %v", err)
	}
	if carried.ID != "t_system" || carried.Name != "system" || carried.Status != 1 {
		t.Errorf("the migration carried the row as %+v, want it unchanged", carried)
	}
	if !carried.CreatedAt.Valid || carried.CreatedAt.String == "" {
		t.Errorf("the carried row reads no creation time, want the migration's default instant")
	}
}

func TestPutRecordsTheCreationTimeInUTC(t *testing.T) {
	r := rosterOver(t, "")
	r.handle.NowFunc = func() time.Time {
		return time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("UTC+8", 8*60*60))
	}
	if err := r.Put(context.Background(), tenant.Tenant{
		ID: "t_put", Name: "put", Status: tenant.StatusActive,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	created := readCreatedAt(t, r, "t_put")
	if created.IsZero() {
		t.Fatal("a row inserted through Put reads no creation time, want the moment of registration")
	}
	if _, offset := created.Zone(); offset != 0 {
		t.Errorf("the creation time is %v, want UTC", created)
	}

	if err := r.Put(context.Background(), tenant.Tenant{
		ID: "t_put", Name: "put", Status: tenant.StatusDisabled,
	}); err != nil {
		t.Fatalf("rewriting through Put: %v", err)
	}
	rewritten := readCreatedAt(t, r, "t_put")
	if !rewritten.Equal(created) {
		t.Errorf("the creation time became %v after a rewrite, want %v", rewritten, created)
	}
}
