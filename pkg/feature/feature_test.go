package feature

import "testing"

// TestValidName holds the shape rule to the cases its wording singles out. A
// single segment, an upper-case letter and an empty segment are each refused,
// and a three-segment name in the namespace form passes; a rule that accepted
// every string, or only checked the first character, would answer at least one
// of them the other way.
func TestValidName(t *testing.T) {
	for _, c := range []struct {
		name string
		want bool
	}{
		{"objectstore", false},    // one segment
		{"Objectstore.x", false},  // upper-case first letter
		{"a..b", false},           // empty segment
		{"authn.admin.sso", true}, // three segments
		{"a.b", true},             // the smallest legal name
		{"a-b.c-1.d", true},       // dashes and digits after the first letter
		{"a.B", false},            // upper-case later segment
		{"a.-b", false},           // segment beginning with a dash
		{"1a.b", false},           // segment beginning with a digit
		{"a.b.", false},           // trailing dot
		{".a.b", false},           // leading dot
		{"a.b_c", false},          // underscore is not in the alphabet
		{"", false},               // empty
		{"a. b", false},           // space is not in the alphabet
		{"a.b-c.d9", true},        // digits and dashes in a later segment
		{"a--b.c", true},          // repeated dashes are still the alphabet
		{"a.b-c-d.e-f-g", true},   // the alphabet across several segments
		{"a-", false},             // one segment with a trailing dash
		{"a.b\n", false},          // a newline is not in the alphabet
	} {
		if got := ValidName(c.name); got != c.want {
			t.Errorf("ValidName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
