package mount

import "testing"

// TestRelativePathStripsTheBase holds the translation between an operation's
// absolute path and the path a Mounter joins to its own base.
func TestRelativePathStripsTheBase(t *testing.T) {
	for _, c := range []struct{ base, absolute, want string }{
		{"/api/v1/auth", "/api/v1/auth/session", "/session"},
		{"/api/v1/system/auth", "/api/v1/system/auth/accounts/:id", "/accounts/:id"},
		{"", "/session", "/session"},
	} {
		got, err := RelativePath(c.base, c.absolute)
		if err != nil {
			t.Errorf("RelativePath(%q, %q): %v", c.base, c.absolute, err)
			continue
		}
		if got != c.want {
			t.Errorf("RelativePath(%q, %q) = %q, want %q", c.base, c.absolute, got, c.want)
		}
	}
}

// TestRelativePathRefusesARouteOutsideTheBase holds the refusal: a declaration
// that does not sit under the mounted base would register somewhere the
// declaration does not describe.
func TestRelativePathRefusesARouteOutsideTheBase(t *testing.T) {
	for _, c := range []struct{ base, absolute string }{
		{"/api/v1/auth", "/api/v1/authz/session"},
		{"/api/v1/auth", "/api/v1/auth"},
		{"/api/v1/auth", "/session"},
	} {
		if _, err := RelativePath(c.base, c.absolute); err == nil {
			t.Errorf("RelativePath(%q, %q) accepted a route outside the base", c.base, c.absolute)
		}
	}
}
