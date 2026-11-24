package feature

import "testing"

// TestValidName checks that ValidName accepts exactly the names the shape rule
// allows.
func TestValidName(t *testing.T) {
	for _, c := range []struct {
		name string
		want bool
	}{
		{"objectstore", false},
		{"Objectstore.x", false},
		{"a..b", false},
		{"authn.admin.sso", true},
		{"a.b", true},
		{"a-b.c-1.d", true},
		{"a.B", false},
		{"a.-b", false},
		{"1a.b", false},
		{"a.b.", false},
		{".a.b", false},
		{"a.b_c", false},
		{"", false},
		{"a. b", false},
		{"a.b-c.d9", true},
		{"a--b.c", true},
		{"a.b-c-d.e-f-g", true},
		{"a-", false},
		{"a.b\n", false},
	} {
		if got := ValidName(c.name); got != c.want {
			t.Errorf("ValidName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
