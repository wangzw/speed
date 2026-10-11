package account

import (
	"errors"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
)

func TestNormalizeIdentifierVectors(t *testing.T) {
	longLocal := strings.Repeat("a", 254-len("@x.io"))
	for _, c := range []struct {
		name  string
		kind  authn.IdentifierKind
		in    string
		want  string
		valid bool
	}{
		// Usernames.
		{"a username is lower-cased", authn.IdentifierUsername, "Alice", "alice", true},
		{"the compatibility characters of NFKC fold to ascii", authn.IdentifierUsername, "ＡＬＩＣＥ０１", "alice01", true},
		{"a ligature folds to its letters", authn.IdentifierUsername, "ﬁnn", "finn", true},
		{"dots, dashes and underscores are allowed inside", authn.IdentifierUsername, "a.b-c_d", "a.b-c_d", true},
		{"three characters is the shortest", authn.IdentifierUsername, "abc", "abc", true},
		{"two characters is too short", authn.IdentifierUsername, "ab", "", false},
		{"sixty-four characters is the longest", authn.IdentifierUsername, strings.Repeat("a", 64), strings.Repeat("a", 64), true},
		{"sixty-five characters is too long", authn.IdentifierUsername, strings.Repeat("a", 65), "", false},
		{"a username holds no @", authn.IdentifierUsername, "al@ice", "", false},
		{"an @ that NFKC makes out of a compatibility character is refused too", authn.IdentifierUsername, "al＠ice", "", false},
		{"it does not begin with a dot", authn.IdentifierUsername, ".alice", "", false},
		{"it does not begin with a dash", authn.IdentifierUsername, "-alice", "", false},
		{"the static prefix is the static table's", authn.IdentifierUsername, "static.root", "", false},
		{"the static prefix in capitals is too once folded", authn.IdentifierUsername, "STATIC.root", "", false},
		{"a UUID is an account identifier's shape", authn.IdentifierUsername, "01234567-89ab-cdef-0123-456789abcdef", "", false},
		{"a UUID in capitals is too", authn.IdentifierUsername, "01234567-89AB-CDEF-0123-456789ABCDEF", "", false},
		{"a UUID without its dashes is too", authn.IdentifierUsername, "0123456789abcdef0123456789abcdef", "", false},
		{"a name that only starts like a UUID is a name", authn.IdentifierUsername, "01234567-89ab-cdef-0123-4567", "01234567-89ab-cdef-0123-4567", true},
		{"a space is refused", authn.IdentifierUsername, "al ice", "", false},
		{"a non-ascii letter that NFKC keeps is refused", authn.IdentifierUsername, "älice", "", false},
		{"the empty username", authn.IdentifierUsername, "", "", false},

		// Email addresses.
		{"an email address is lower-cased", authn.IdentifierEmail, "Alice@Example.COM", "alice@example.com", true},
		{"surrounding space is trimmed", authn.IdentifierEmail, "  alice@example.com\t", "alice@example.com", true},
		{"a sub-address is kept", authn.IdentifierEmail, "alice+tag@example.com", "alice+tag@example.com", true},
		{"a dot is kept", authn.IdentifierEmail, "a.l.i.c.e@example.com", "a.l.i.c.e@example.com", true},
		{"a non-ascii local part is refused", authn.IdentifierEmail, "älice@example.com", "", false},
		{"a non-ascii domain is refused: no IDN conversion", authn.IdentifierEmail, "alice@exämple.com", "", false},
		{"a fullwidth @ is not an @", authn.IdentifierEmail, "alice＠example.com", "", false},
		{"two @ are refused", authn.IdentifierEmail, "a@b@example.com", "", false},
		{"no @ is refused", authn.IdentifierEmail, "alice.example.com", "", false},
		{"an empty local part is refused", authn.IdentifierEmail, "@example.com", "", false},
		{"an empty domain is refused", authn.IdentifierEmail, "alice@", "", false},
		{"a space inside is refused", authn.IdentifierEmail, "al ice@example.com", "", false},
		{"a control character inside is refused", authn.IdentifierEmail, "al\x00ice@example.com", "", false},
		{"DEL is not a printable character", authn.IdentifierEmail, "al\x7fice@example.com", "", false},
		{"254 bytes is the longest", authn.IdentifierEmail, longLocal + "@x.io", longLocal + "@x.io", true},
		{"255 bytes is too long", authn.IdentifierEmail, "a" + longLocal + "@x.io", "", false},
		{"the empty email address", authn.IdentifierEmail, "", "", false},

		// Phone numbers.
		{"an E.164 number", authn.IdentifierPhone, "+8613812345678", "+8613812345678", true},
		{"seven digits is the shortest", authn.IdentifierPhone, "+1234567", "+1234567", true},
		{"six digits is too short", authn.IdentifierPhone, "+123456", "", false},
		{"fifteen digits is the longest", authn.IdentifierPhone, "+123456789012345", "+123456789012345", true},
		{"sixteen digits is too long", authn.IdentifierPhone, "+1234567890123456", "", false},
		{"a country code does not begin with zero", authn.IdentifierPhone, "+0123456789", "", false},
		{"the plus sign is required", authn.IdentifierPhone, "8613812345678", "", false},
		{"a national form is refused", authn.IdentifierPhone, "013812345678", "", false},
		{"a space is refused", authn.IdentifierPhone, "+86 13812345678", "", false},
		{"a dash is refused", authn.IdentifierPhone, "+86-13812345678", "", false},
		{"surrounding space is refused: nothing is trimmed", authn.IdentifierPhone, " +8613812345678", "", false},
		{"a fullwidth digit is refused: NFKC is not applied", authn.IdentifierPhone, "+８613812345678", "", false},
		{"the empty number", authn.IdentifierPhone, "", "", false},

		// Kinds.
		{"a kind that is none of the three", authn.IdentifierKind("fax"), "alice", "", false},
		{"the empty kind", authn.IdentifierKind(""), "alice", "", false},
	} {
		got, err := NormalizeIdentifier(c.kind, c.in)
		switch {
		case c.valid && err != nil:
			t.Errorf("%s: %q was refused: %v", c.name, c.in, err)
		case c.valid && got != c.want:
			t.Errorf("%s: %q normalized to %q, want %q", c.name, c.in, got, c.want)
		case !c.valid && !errors.Is(err, authn.ErrInvalidIdentifier):
			t.Errorf("%s: %q gave %q and %v, want authn.ErrInvalidIdentifier", c.name, c.in, got, err)
		case !c.valid && got != "":
			t.Errorf("%s: a refusal returned the value %q", c.name, got)
		}
	}
}

func TestANormalizationFailureNamesNoValue(t *testing.T) {
	for _, in := range []struct {
		kind  authn.IdentifierKind
		value string
	}{
		{authn.IdentifierEmail, "secret-person@exämple.com"},
		{authn.IdentifierUsername, "static.secret-person"},
		{authn.IdentifierPhone, "+0123456789"},
	} {
		_, err := NormalizeIdentifier(in.kind, in.value)
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "0123456789") {
			t.Errorf("the failure for a %s is %v, which must name the class and not the value", in.kind, err)
		}
	}
}

// Normalization is a fixed point: a value that is stored is read back in the same
// form, which is what lets the write and the lookup agree.
func TestNormalizationIsIdempotent(t *testing.T) {
	for _, c := range []struct {
		kind  authn.IdentifierKind
		value string
	}{
		{authn.IdentifierUsername, "ＡＬＩＣＥ０１"},
		{authn.IdentifierEmail, "  Alice+Tag@Example.COM "},
		{authn.IdentifierPhone, "+8613812345678"},
	} {
		once, err := NormalizeIdentifier(c.kind, c.value)
		if err != nil {
			t.Fatalf("%s %q: %v", c.kind, c.value, err)
		}
		twice, err := NormalizeIdentifier(c.kind, once)
		if err != nil || twice != once {
			t.Errorf("%s: normalizing %q again gave %q and %v", c.kind, once, twice, err)
		}
	}
}

func TestNormalizePrefix(t *testing.T) {
	for _, c := range []struct {
		name string
		kind authn.IdentifierKind
		in   string
		want string
		err  error
	}{
		{"a username prefix is folded", authn.IdentifierUsername, "ＡLI", "ali", nil},
		{"an email prefix is folded and trimmed", authn.IdentifierEmail, " Ali@Exa", "ali@exa", nil},
		{"a phone prefix is a plus sign and digits", authn.IdentifierPhone, "+861", "+861", nil},
		{"two characters are too few", authn.IdentifierUsername, "al", "", authn.ErrInvalidArgument},
		{"two characters of a phone prefix are too few", authn.IdentifierPhone, "+8", "", authn.ErrInvalidArgument},
		{"a pattern character is not a username character", authn.IdentifierUsername, "al%", "", authn.ErrInvalidIdentifier},
		{"a pattern character is not an email character either: nothing is matched by a pattern", authn.IdentifierEmail, "ali%", "ali%", nil},
		{"a prefix of two @ is refused", authn.IdentifierEmail, "a@b@c", "", authn.ErrInvalidIdentifier},
		{"a non-ascii email prefix is refused", authn.IdentifierEmail, "älice", "", authn.ErrInvalidIdentifier},
		{"a phone prefix without a plus sign is refused", authn.IdentifierPhone, "8613", "", authn.ErrInvalidIdentifier},
		{"an unknown kind", authn.IdentifierKind("fax"), "alice", "", authn.ErrInvalidIdentifier},
	} {
		got, err := normalizePrefix(c.kind, c.in)
		if c.err == nil && (err != nil || got != c.want) {
			t.Errorf("%s: %q gave %q and %v, want %q", c.name, c.in, got, err, c.want)
		}
		if c.err != nil && !errors.Is(err, c.err) {
			t.Errorf("%s: %q gave %q and %v, want %v", c.name, c.in, got, err, c.err)
		}
	}
}

func TestPrefixBoundIsTheLeastStringAboveEveryExtension(t *testing.T) {
	for _, c := range []struct{ prefix, want string }{
		{"abc", "abd"}, {"ab9", "ab:"}, {"+861", "+862"}, {"a@b", "a@c"},
	} {
		if got := prefixBound(c.prefix); got != c.want {
			t.Errorf("prefixBound(%q) = %q, want %q", c.prefix, got, c.want)
		}
		for _, extension := range []string{"", "a", "zzzz", "~~~~", "\x7e"} {
			if c.prefix+extension < c.prefix || c.prefix+extension >= prefixBound(c.prefix) {
				t.Errorf("%q is not in [%q, %q)", c.prefix+extension, c.prefix, prefixBound(c.prefix))
			}
		}
	}
}
