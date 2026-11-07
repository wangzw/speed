package audit_test

import (
	"testing"

	"github.com/vislake/speed/pkg/audit"
)

// TestAccountObjectNamesTheType holds the one spelling an entry whose object
// names an account uses: the type marker, then the identifier unchanged. A
// reader relies on the marker to tell an account identifier from a session or
// an invitation identifier, whose opaque shapes are alike.
func TestAccountObjectNamesTheType(t *testing.T) {
	const id = "019a2d4f-1c3e-7b52-9f00-6d7a1b2c3d4e"
	if got := audit.AccountObject(id); got != "account:"+id {
		t.Errorf("AccountObject(%q) = %q, want %q", id, got, "account:"+id)
	}
}

// TestAccountObjectLeavesNoMarkerWithoutAnAccount holds that an entry naming
// no account carries an empty object: a bare marker would name an account
// whose identifier is empty, which is no account at all.
func TestAccountObjectLeavesNoMarkerWithoutAnAccount(t *testing.T) {
	if got := audit.AccountObject(""); got != "" {
		t.Errorf("AccountObject(%q) = %q, want the empty object", "", got)
	}
}
