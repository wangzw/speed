package budgetalias

import "testing"

// TestCanonical folds a mailbox into one budget form: the "+suffix" is dropped,
// and the two Gmail domains also fold their dots and become one domain.
func TestCanonical(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"plain", "ada@example.com", "ada@example.com"},
		{"case", "Ada@Example.COM", "ada@example.com"},
		{"plus suffix", "ada+promo@example.com", "ada@example.com"},
		{"plus suffix with another plus", "ada+promo+more@example.com", "ada@example.com"},
		{"dots below Gmail stay", "a.b@example.com", "a.b@example.com"},
		{"dots in Gmail fold", "ada.lovelace@gmail.com", "adalovelace@gmail.com"},
		{"plus and dots in Gmail", "ada.lovelace+promo@gmail.com", "adalovelace@gmail.com"},
		{"googlemail folds to gmail", "ada@googlemail.com", "ada@gmail.com"},
		{"dots and googlemail", "ada.lovelace@googlemail.com", "adalovelace@gmail.com"},
		{"a subdomain is not Gmail", "ada@mail.gmail.com", "ada@mail.gmail.com"},
		{"no at sign", "ada", "ada"},
		{"empty", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Canonical(c.in); got != c.want {
				t.Errorf("Canonical(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
