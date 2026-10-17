package metadata

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/core"
	"github.com/vislake/speed/pkg/objectstore"
)

// nameCases is the normalization contract expressed as input -> stored value.
// The reviewer's case leads: a path-traversal shape with a real newline and a
// backslash, which must store as a readable label rather than fail or land a
// separator.
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

// TestNormalizeNameFollowsTheDesignRules pins the normalization rules directly:
// separators, dropped categories, trimming, the all-dots rule and the byte cap.
func TestNormalizeNameFollowsTheDesignRules(t *testing.T) {
	for _, c := range nameCases {
		if got := normalizeName(c.input); got != c.want {
			t.Errorf("%s: normalizeName(%q) = %q, want %q", c.label, c.input, got, c.want)
		}
	}

	// Only dots after the cut: a 300-dot name is truncated to the cap and
	// then stores as nothing.
	if got := normalizeName(strings.Repeat(".", 300)); got != "" {
		t.Errorf("normalizeName of 300 dots = %q, want the empty string", got)
	}
}

// TestNormalizeNameCutsAtACharacterBoundaryAndNeverExceedsTheCap holds the byte
// rule: a multi-byte character at the limit is never split, the stored value is
// valid UTF-8, and no value exceeds the cap.
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

	// A name that fits exactly at the cap is kept whole.
	exact := strings.Repeat("é", 127) + "x"
	if len(exact) != nameMaxBytes {
		t.Fatalf("the exact-fit fixture is %d bytes, want %d", len(exact), nameMaxBytes)
	}
	if got := normalizeName(exact); got != exact {
		t.Errorf("normalizeName of a name exactly at the cap changed it")
	}

	// Whitespace that survives the cut is trimmed again afterwards.
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

// TestCreateStoresTheNormalizedNameAndNeverRefusesIt holds the end-to-end rule:
// every name, including the reviewer's path-shaped one, is accepted by Create,
// stored normalized, and read back normalized by Get and List.
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
					// The name is a label, never a validation failure.
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

			// The reviewer's case, in the terms the acceptance states it: the
			// stored and viewed name carries no path separator or control
			// character and is readable text.
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

// TestTheObjectViewAlwaysCarriesTheName holds that the wire view has a name
// member even when the object has none, so a client reads one shape and falls
// back to the identifier rather than to an absent field.
func TestTheObjectViewAlwaysCarriesTheName(t *testing.T) {
	raw, err := json.Marshal(objectViewOf(objectstore.Object{ID: "o-1"}))
	if err != nil {
		t.Fatalf("encoding the view: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decoding the view: %v", err)
	}
	name, ok := fields["name"]
	if !ok {
		t.Fatalf("the object view carries no name member: %s", raw)
	}
	if name != "" {
		t.Errorf("an unnamed object's view carries the name %v, want the empty string", name)
	}
}
