package metadata

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	sqlitedriver "github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/db/sqlite"
	"github.com/vislake/speed/pkg/objectstore"
)

var nameCases = []struct {
	label string
	input string
	want  string
}{
	{"path traversal with a newline and a backslash", "../evil\n\\name.txt", ".._evil_name.txt"},
	{"right-to-left override", "report\u202efdp.txt", "reportfdp.txt"},
	{"zero-width space", "zero\u200bwidth", "zerowidth"},
	{"line separator", "line\u2028separator", "lineseparator"},
	{"paragraph separator", "para\u2029graph", "paragraph"},
	{"padded", "  padded  ", "padded"},
	{"mixed whitespace", " \t padded \n ", "padded"},
	{"only dots", "...", ""},
	{"one dot", ".", ""},
	{"empty", "", ""},
	{"only whitespace", "   ", ""},
	{"both separators", "a/b\\c", "a_b_c"},
	{"quotes and a semicolon", `"quoted"; semi`, `"quoted"; semi`},
	{"extension kept", "archive.tar.gz", "archive.tar.gz"},
	{"invalid UTF-8", "a\xffb", "ab"},
}

// TestNormalizeNameFollowsTheDesignRules pins the normalization rules directly: separators,
// dropped categories, trimming, the all-dots rule and the byte cap.
func TestNormalizeNameFollowsTheDesignRules(t *testing.T) {
	for _, c := range nameCases {
		if got := normalizeName(c.input); got != c.want {
			t.Errorf("%s: normalizeName(%q) = %q, want %q", c.label, c.input, got, c.want)
		}
	}

	if got := normalizeName(strings.Repeat(".", 300)); got != "" {
		t.Errorf("normalizeName of 300 dots = %q, want the empty string", got)
	}
}

// TestNormalizeNameCutsAtACharacterBoundaryAndNeverExceedsTheCap checks that a long name is cut at a
// rune boundary within the cap, an exact fit is untouched and whitespace is trimmed.
func TestNormalizeNameCutsAtACharacterBoundaryAndNeverExceedsTheCap(t *testing.T) {
	long := strings.Repeat("é", 200)
	got := normalizeName(long)
	if !utf8.ValidString(got) {
		t.Errorf("a cut name is not valid UTF-8: %q", got)
	}
	if len(got) > nameMaxBytes {
		t.Errorf("a cut name is %d bytes, above the %d cap", len(got), nameMaxBytes)
	}
	if want := strings.Repeat("é", 127); got != want {
		t.Errorf("normalizeName(200 two-byte characters) cut to %d bytes; want the first 127 characters", len(got))
	}

	exact := strings.Repeat("é", 127) + "x"
	if len(exact) != nameMaxBytes {
		t.Fatalf("the exact-fit fixture is %d bytes, want %d", len(exact), nameMaxBytes)
	}
	if got := normalizeName(exact); got != exact {
		t.Errorf("normalizeName of a name exactly at the cap changed it")
	}

	trailing := strings.Repeat("a", 200) + strings.Repeat(" ", 100)
	if got, want := normalizeName(trailing), strings.Repeat("a", 200); got != want {
		t.Errorf("normalizeName trimmed to %d bytes, want the 200 characters before the whitespace", len(got))
	}

	for _, c := range nameCases {
		if got := normalizeName(c.input); len(got) > nameMaxBytes {
			t.Errorf("normalizeName(%q) is %d bytes, above the cap", c.input, len(got))
		}
	}
}

// TestCreateStoresTheNormalizedNameAndNeverRefusesIt checks that Create stores the normalized name
// for every case, including path separators and control characters, and never refuses one.
func TestCreateStoresTheNormalizedNameAndNeverRefusesIt(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e engine) {
		h := e.on(t, harness{})
		h.drive(t, func(ctx context.Context, reg *core.Registry) error {
			objects := objectsOf(t, reg)
			tctx := tenantCtx(t, reg, "acme")

			for _, c := range nameCases {
				declared, err := objects.Create(tctx, objectstore.Declaration{
					Name: c.input, Size: 1, ContentType: "text/plain",
				})
				if err != nil {
					t.Fatalf("%s: Create refused the name %q: %v", c.label, c.input, err)
				}
				if declared.Name != c.want {
					t.Errorf("%s: Create of %q returned the name %q, want %q", c.label, c.input, declared.Name, c.want)
				}

				got, err := objects.Get(tctx, declared.ID)
				if err != nil {
					t.Fatalf("%s: reading back %q: %v", c.label, c.input, err)
				}
				if got.Name != c.want {
					t.Errorf("%s: Get of %q answered the name %q, want %q", c.label, c.input, got.Name, c.want)
				}

				var stored string
				if err := dbHandle(t, reg).Raw(
					"SELECT name FROM objects WHERE tenant_id = ? AND id = ?",
					"acme", declared.ID,
				).Row().Scan(&stored); err != nil {
					t.Fatalf("%s: reading the stored name of %q: %v", c.label, c.input, err)
				}
				if stored != c.want {
					t.Errorf("%s: the stored name of %q is %q, want %q", c.label, c.input, stored, c.want)
				}
			}

			declared, err := objects.Create(tctx, objectstore.Declaration{
				Name: "../evil\n\\name.txt", Size: 1, ContentType: "text/plain",
			})
			if err != nil {
				t.Fatalf("Create refused the reviewer's name: %v", err)
			}
			listed, err := objects.List(tctx, objectstore.ListQuery{Limit: maxListLimit})
			if err != nil {
				t.Fatalf("listing: %v", err)
			}
			found := false
			for _, o := range listed {
				if o.ID != declared.ID {
					continue
				}
				found = true
				if o.Name == "" || !utf8.ValidString(o.Name) {
					t.Errorf("the listed name %q is empty or not valid UTF-8", o.Name)
				}
				for _, r := range o.Name {
					if r == '/' || r == '\\' || unicode.IsControl(r) {
						t.Errorf("the listed name %q carries the unsafe character %U", o.Name, r)
					}
				}
			}
			if !found {
				t.Fatalf("the list did not carry the object %s", declared.ID)
			}
			return nil
		})
	})
}

// TestARowThatPredatesTheNameMigrationReadsAsUnnamed checks that a row written before the name
// column reads as an empty name through Get and List.
func TestARowThatPredatesTheNameMigrationReadsAsUnnamed(t *testing.T) {
	schema, err := fs.ReadFile(migrationRoot, "migrations/sqlite/0001_objects.sql")
	if err != nil {
		t.Fatalf("reading the first migration: %v", err)
	}
	path := filepath.Join(t.TempDir(), "objects.db")
	handle, err := gorm.Open(sqlitedriver.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening the pre-migration database: %v", err)
	}
	pool, err := handle.DB()
	if err != nil {
		t.Fatalf("taking the pre-migration pool: %v", err)
	}
	if _, err := pool.Exec(string(schema)); err != nil {
		t.Fatalf("creating the pre-migration table: %v", err)
	}
	if _, err := pool.Exec(`INSERT INTO objects
		(id, tenant_id, key, state, declared_size, declared_type, declared_checksum,
		 size, mime, checksum_sha256, upload_expires_at, expires_at, created_at, updated_at)
		VALUES ('legacy-1', 'acme', 'acme/legacy-1', 'completed', 3, 'text/plain', '',
			3, 'text/plain', '', 0, NULL, 1, 1)`); err != nil {
		t.Fatalf("writing the pre-migration row: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("closing the pre-migration pool: %v", err)
	}

	h := harness{database: sqlite.Module(), section: "sqlite", dsn: path}
	h.drive(t, func(_ context.Context, reg *core.Registry) error {
		objects := objectsOf(t, reg)
		tctx := tenantCtx(t, reg, "acme")
		got, err := objects.Get(tctx, "legacy-1")
		if err != nil {
			t.Fatalf("reading the pre-migration row: %v", err)
		}
		if got.Name != "" {
			t.Errorf("the pre-migration row carries the name %q, want the empty string", got.Name)
		}
		listed, err := objects.List(tctx, objectstore.ListQuery{})
		if err != nil {
			t.Fatalf("listing: %v", err)
		}
		for _, o := range listed {
			if o.ID == "legacy-1" && o.Name != "" {
				t.Errorf("the listed pre-migration row carries the name %q, want the empty string", o.Name)
			}
		}
		return nil
	})
}
