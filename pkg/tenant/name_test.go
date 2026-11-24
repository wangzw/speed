package tenant

import (
	"strings"
	"testing"
)

// TestIsNameHoldsTheSlugForm checks the slug form of three to sixty-three
// lower-case letters, digits and dashes, starting with a letter or a digit.
func TestIsNameHoldsTheSlugForm(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want bool
	}{
		{"three letters", "abc", true},
		{"a dash inside", "a-b", true},
		{"dashes repeated", "a--b", true},
		{"a trailing dash", "abc-", true},
		{"sixty-three characters", strings.Repeat("a", 63), true},
		{"two characters", "ab", false},
		{"one character", "a", false},
		{"the empty name", "", false},
		{"an upper-case letter", "Abc", false},
		{"an underscore", "a_b", false},
		{"a dot", "a.b", false},
		{"a leading dash", "-abc", false},
		{"a blank", "a b", false},
		{"a control character", "a\tb", false},
		{"sixty-four characters", strings.Repeat("a", 64), false},
	} {
		if got := IsName(c.in); got != c.want {
			t.Errorf("%s: IsName(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}
